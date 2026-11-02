package client_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/client"
	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
)

func TestNetshBackend_Apply(t *testing.T) {
	t.Parallel()

	run := &fakeRun{}
	dir := t.TempDir()
	checkedMarker := false
	runWithMarkerCheck := func(ctx context.Context, input, name string, args ...string) ([]byte, error) {
		if name == "netsh" && !checkedMarker {
			raw, err := os.ReadFile(filepath.Join(dir, "netsh-dns.json")) //#nosec G304
			require.NoError(t, err)
			require.JSONEq(t, `{"iface":"dvpn-c0"}`, string(raw))

			checkedMarker = true
		}

		return run.run(ctx, input, name, args...)
	}
	backend := client.NewNetshBackend(dir, runWithMarkerCheck)

	err := backend.Apply(t.Context(), "dvpn-c0", []string{
		"203.0.113.1", "2001:db8::1", "::ffff:203.0.113.2",
	})
	require.NoError(t, err)
	require.True(t, checkedMarker)
	require.Equal(t, []fakeCall{
		{name: "netsh", args: []string{"interface", "ipv4", "set", "dnsservers", "name=dvpn-c0",
			"source=static", "address=203.0.113.1", "register=none", "validate=no"}},
		{name: "netsh", args: []string{"interface", "ipv4", "add", "dnsservers", "name=dvpn-c0",
			"address=203.0.113.2", "index=2", "validate=no"}},
		{name: "netsh", args: []string{"interface", "ipv6", "set", "dnsservers", "name=dvpn-c0",
			"source=static", "address=2001:db8::1", "register=none", "validate=no"}},
		{name: "ipconfig", args: []string{"/flushdns"}},
	}, run.calls)

	raw, err := os.ReadFile(filepath.Join(dir, "netsh-dns.json")) //#nosec G304
	require.NoError(t, err)
	require.JSONEq(t, `{"iface":"dvpn-c0"}`, string(raw))

	temps, err := filepath.Glob(filepath.Join(dir, ".netsh-dns.json-*"))
	require.NoError(t, err)
	require.Empty(t, temps)
}

func TestNetshBackend_Apply_CommandFailures(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		fail      string
		wantCalls int
	}{
		{"set", "netsh interface ipv4 set dnsservers name=tunnel source=static " +
			"address=203.0.113.1 register=none validate=no", 1},
		{"add", "netsh interface ipv4 add dnsservers name=tunnel address=203.0.113.2 index=2 validate=no", 2},
		{"flush", "ipconfig /flushdns", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			run := &fakeRun{errs: map[string]error{tc.fail: errors.New("command failed")}}
			dir := t.TempDir()
			backend := client.NewNetshBackend(dir, run.run)

			err := backend.Apply(t.Context(), "tunnel", []string{"203.0.113.1", "203.0.113.2"})
			require.ErrorContains(t, err, "command failed")
			require.Len(t, run.calls, tc.wantCalls)
			require.FileExists(t, filepath.Join(dir, "netsh-dns.json"))
		})
	}
}

func TestNetshBackend_Apply_MarkerWriteFailure(t *testing.T) {
	t.Parallel()

	run := &fakeRun{}
	backend := client.NewNetshBackend(filepath.Join(t.TempDir(), "missing"), run.run)

	err := backend.Apply(t.Context(), "tunnel", []string{"203.0.113.1"})
	require.ErrorContains(t, err, "creating netsh dns marker")
	require.Empty(t, run.calls)
}

func TestNetshBackend_Apply_RejectsBadServer(t *testing.T) {
	t.Parallel()

	run := &fakeRun{}
	err := client.NewNetshBackend(t.TempDir(), run.run).Apply(t.Context(), "tunnel", []string{"dns.example.com"})
	require.ErrorContains(t, err, "dns.example.com")
	require.Empty(t, run.calls)
}

func TestNetshBackend_Cleanup_AbsentInterfaceFlushFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	run := &fakeRun{}
	backend := client.NewNetshBackend(dir, run.run)
	require.NoError(t, backend.Apply(t.Context(), "no-such-interface", []string{"203.0.113.1"}))

	run.calls = nil
	run.errs = map[string]error{"ipconfig /flushdns": errors.New("flush failed")}

	require.ErrorContains(t, backend.Cleanup(t.Context()), "flush failed")
	require.Equal(t, []string{"ipconfig /flushdns"}, run.commands())
	require.FileExists(t, filepath.Join(dir, "netsh-dns.json"))
}

func TestNetshBackend_Cleanup_BadMarker(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	marker := filepath.Join(dir, "netsh-dns.json")
	require.NoError(t, os.WriteFile(marker, []byte("{"), 0o600))

	run := &fakeRun{}
	backend := client.NewNetshBackend(dir, run.run)

	err := backend.Cleanup(t.Context())
	require.ErrorContains(t, err, "decoding netsh dns marker")
	require.Empty(t, run.calls)
	require.FileExists(t, marker)
}

func TestNetshBackend_Cleanup_ConfigSwitchAndRetry(t *testing.T) {
	t.Parallel()

	ifaces, err := net.Interfaces()
	require.NoError(t, err)
	require.NotEmpty(t, ifaces)

	name := ifaces[0].Name
	reset4 := "netsh interface ipv4 set dnsservers name=" + name + " source=dhcp"
	reset6 := "netsh interface ipv6 set dnsservers name=" + name + " source=dhcp"

	for _, tc := range []struct {
		name string
		fail string
		want []string
	}{
		{"ipv4 reset", reset4, []string{reset4}},
		{"ipv6 reset", reset6, []string{reset4, reset6}},
		{"cache flush", "ipconfig /flushdns", []string{reset4, reset6, "ipconfig /flushdns"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			run := &fakeRun{}
			active := client.NewNetshBackend(dir, run.run)
			require.NoError(t, active.Apply(t.Context(), name, []string{"203.0.113.1"}))

			marker := filepath.Join(dir, "netsh-dns.json")
			require.FileExists(t, marker)

			run.calls = nil
			run.errs = map[string]error{tc.fail: errors.New("restore failed")}
			selectedNone := client.NewWindowsDNSBackend(config.DNSNone, dir, run.run)
			err := selectedNone.Cleanup(t.Context())
			require.ErrorContains(t, err, "restore failed")
			require.FileExists(t, marker)
			require.Equal(t, tc.want, run.commands())

			delete(run.errs, tc.fail)
			require.NoError(t, selectedNone.Cleanup(t.Context()))

			_, err = os.Stat(marker)
			require.ErrorIs(t, err, os.ErrNotExist)
			require.NoError(t, selectedNone.Cleanup(t.Context()))
		})
	}
}

func TestNetshBackend_Revert(t *testing.T) {
	t.Parallel()

	ifaces, err := net.Interfaces()
	require.NoError(t, err)
	require.NotEmpty(t, ifaces)

	run := &fakeRun{}
	dir := t.TempDir()
	backend := client.NewNetshBackend(dir, run.run)
	require.NoError(t, backend.Revert(t.Context(), "no-such-interface"))
	require.Empty(t, run.calls)

	name := ifaces[0].Name

	require.NoError(t, backend.Revert(t.Context(), name))
	require.Empty(t, run.calls)
	require.NoError(t, backend.Apply(t.Context(), name, []string{"203.0.113.1"}))

	run.calls = nil

	require.NoError(t, backend.Revert(t.Context(), name))
	require.Equal(t, []fakeCall{
		{name: "netsh", args: []string{"interface", "ipv4", "set", "dnsservers", "name=" + name, "source=dhcp"}},
		{name: "netsh", args: []string{"interface", "ipv6", "set", "dnsservers", "name=" + name, "source=dhcp"}},
		{name: "ipconfig", args: []string{"/flushdns"}},
	}, run.calls)
	require.NoError(t, backend.Cleanup(t.Context()))
}

func TestNetshBackend_Revert_FlushFailure(t *testing.T) {
	t.Parallel()

	ifaces, err := net.Interfaces()
	require.NoError(t, err)
	require.NotEmpty(t, ifaces)

	run := &fakeRun{}
	dir := t.TempDir()
	backend := client.NewNetshBackend(dir, run.run)
	require.NoError(t, backend.Apply(t.Context(), ifaces[0].Name, []string{"203.0.113.1"}))

	run.errs = map[string]error{"ipconfig /flushdns": errors.New("command failed")}
	err = backend.Revert(t.Context(), ifaces[0].Name)
	require.ErrorContains(t, err, "command failed")
	require.FileExists(t, filepath.Join(dir, "netsh-dns.json"))
}
