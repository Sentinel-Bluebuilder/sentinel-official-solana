package client_test

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/client"
	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
)

func TestLinuxDNSBackend_Cleanup_RemovesResolvconfBackup(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{config.DNSNone, config.DNSResolvectl, config.DNSResolvconf} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "resolv.conf")
			backup := filepath.Join(t.TempDir(), "resolv.conf.bak")
			require.NoError(t, os.WriteFile(path, []byte("nameserver 192.0.2.1\n"), 0o600))
			require.NoError(t, os.WriteFile(backup, []byte("nameserver 192.0.2.53\n"), 0o600))

			require.NoError(t, client.NewLinuxDNSBackend(kind, path, backup).Cleanup(t.Context()))

			got, err := os.ReadFile(path) //#nosec G304
			require.NoError(t, err)
			require.Equal(t, "nameserver 192.0.2.53\n", string(got))

			_, err = os.Stat(backup)
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestResolvconfBackend_Apply_BacksUpIntoStateDir(t *testing.T) {
	t.Parallel()

	etc, state := t.TempDir(), t.TempDir()
	path := filepath.Join(etc, "resolv.conf")
	backup := filepath.Join(state, "resolv.conf.bak")

	require.NoError(t, os.WriteFile(path, []byte("nameserver 192.0.2.1\n"), 0o600))

	before, err := os.Stat(path)
	require.NoError(t, err)

	backend := client.NewResolvconfBackend(path, backup)
	require.NoError(t, backend.Apply(t.Context(), "dvpn-c0", []string{"192.0.2.2"}))

	saved, err := os.ReadFile(backup) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, "nameserver 192.0.2.1\n", string(saved))

	entries, err := os.ReadDir(etc)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	require.NoError(t, backend.Cleanup(t.Context()))

	restored, err := os.ReadFile(path) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, "nameserver 192.0.2.1\n", string(restored))

	after, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, os.SameFile(before, after))

	_, err = os.Stat(backup)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestResolvconfBackend_Cleanup(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		backup string
		want   string
	}{
		{name: "restores backup", backup: "nameserver 192.0.2.53\n", want: "nameserver 192.0.2.53\n"},
		{name: "without backup", want: "nameserver 192.0.2.1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "resolv.conf")
			backup := filepath.Join(t.TempDir(), "resolv.conf.bak")
			require.NoError(t, os.WriteFile(path, []byte("nameserver 192.0.2.1\n"), 0o600))

			if tc.backup != "" {
				require.NoError(t, os.WriteFile(backup, []byte(tc.backup), 0o600))
			}

			require.NoError(t, client.NewResolvconfBackend(path, backup).Cleanup(t.Context()))

			got, err := os.ReadFile(path) //#nosec G304
			require.NoError(t, err)
			require.Equal(t, tc.want, string(got))

			_, err = os.Stat(backup)
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestResolvconfBackend_Cleanup_RestoreFailureKeepsBackup(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "resolv.conf")
	require.NoError(t, os.Mkdir(path, 0o700))
	backup := filepath.Join(t.TempDir(), "resolv.conf.bak")
	require.NoError(t, os.WriteFile(backup, []byte("nameserver 192.0.2.53\n"), 0o600))

	require.Error(t, client.NewResolvconfBackend(path, backup).Cleanup(t.Context()))

	got, err := os.ReadFile(backup) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, "nameserver 192.0.2.53\n", string(got))
}

func TestResolvconfBackend_Cleanup_SymlinkedResolvConf(t *testing.T) {
	t.Parallel()

	etc, state := t.TempDir(), t.TempDir()
	target := filepath.Join(etc, "stub-resolv.conf")
	path := filepath.Join(etc, "resolv.conf")
	backup := filepath.Join(state, "resolv.conf.bak")

	require.NoError(t, os.WriteFile(target, []byte("nameserver 127.0.0.53\n"), 0o600))
	require.NoError(t, os.Symlink(target, path))

	backend := client.NewResolvconfBackend(path, backup)
	require.NoError(t, backend.Apply(t.Context(), "dvpn-c0", []string{"192.0.2.2"}))

	applied, err := os.ReadFile(target) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, "nameserver 192.0.2.2\n", string(applied))

	require.NoError(t, backend.Cleanup(t.Context()))

	link, err := os.Readlink(path)
	require.NoError(t, err)
	require.Equal(t, target, link)

	restored, err := os.ReadFile(target) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, "nameserver 127.0.0.53\n", string(restored))
}

func TestRuntime_ApplyDNS_InvalidServerLeavesInterfaceUntouched(t *testing.T) {
	t.Parallel()

	ifaces, err := net.Interfaces()
	require.NoError(t, err)
	require.NotEmpty(t, ifaces)

	fakeRPC := sdktest.NewFakeRPC(t)
	home := testutil.NewClientHome(t, fakeRPC)
	home.Loaded.Client.Network.DNSServers = []string{"dns.example.com"}
	rt := newRuntime(t, home, home.ProgramClient)
	run := &fakeRun{}
	dir := t.TempDir()
	client.SetDNSBackend(rt, client.NewNetshBackend(dir, run.run))

	err = client.ApplyDNS(t.Context(), rt, ifaces[0].Name)
	require.ErrorContains(t, err, "dns.example.com")
	require.Empty(t, run.calls)

	_, err = os.Stat(filepath.Join(dir, "netsh-dns.json"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRuntime_ApplyDNS_OtherInterfaceMarkerUntouched(t *testing.T) {
	t.Parallel()

	ifaces, err := net.Interfaces()
	require.NoError(t, err)
	require.NotEmpty(t, ifaces)

	fakeRPC := sdktest.NewFakeRPC(t)
	home := testutil.NewClientHome(t, fakeRPC)
	rt := newRuntime(t, home, home.ProgramClient)
	dir := t.TempDir()
	marker := filepath.Join(dir, "netsh-dns.json")
	require.NoError(t, os.WriteFile(marker, []byte(`{"iface":"absent-owned-interface"}`), 0o600))

	run := &fakeRun{errs: map[string]error{"ipconfig /flushdns": errors.New("restore failed")}}
	client.SetDNSBackend(rt, client.NewNetshBackend(dir, run.run))

	err = client.ApplyDNS(t.Context(), rt, ifaces[0].Name)
	require.ErrorContains(t, err, "restore failed")
	require.ErrorContains(t, err, "marker belongs to")
	require.Equal(t, []string{"ipconfig /flushdns"}, run.commands())
	require.FileExists(t, marker)
}

func TestRuntime_ApplyDNS_PartialFailureReverts(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	home := testutil.NewClientHome(t, fakeRPC)
	rt := newRuntime(t, home, home.ProgramClient)
	revertErr := errors.New("revert failed")
	backend := &recordingDNSBackend{failFrom: 1, revertErr: revertErr}
	client.SetDNSBackend(rt, backend)

	err := client.ApplyDNS(t.Context(), rt, "tunnel")
	require.ErrorIs(t, err, errDNSApplyFailed)
	require.ErrorIs(t, err, revertErr)
	require.Equal(t, []string{"tunnel"}, backend.reverts())
}
