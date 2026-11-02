package main_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	main "github.com/sentinel-official/sentinel-dvpnd/cmd/sentinel-dvpnd"
	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-dvpnd/internal/preflight"
	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/wireguard"
)

func TestCheckInstancePermissions(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == goosWindows {
		t.Skip("windows reports file permissions differently")
	}

	dir := t.TempDir()
	keys := filepath.Join(dir, "keys")
	key := filepath.Join(keys, "operator.json")

	require.NoError(t, os.Mkdir(keys, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), nil, 0o600))
	require.NoError(t, os.WriteFile(key, nil, 0o600))

	var listenConfig net.ListenConfig

	socket := filepath.Join(dir, "daemon.sock")
	listener, err := listenConfig.Listen(t.Context(), "unix", socket)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	require.NoError(t, os.Chmod(socket, 0o660)) //#nosec G302
	require.NoError(t, os.Chmod(dir, 0o750))    //#nosec G302

	gid := os.Getgid()
	require.Equal(t, service.SeverityOK, main.CheckInstancePermissions(dir, gid).Severity)

	link := filepath.Join(t.TempDir(), "instance")
	require.NoError(t, os.Symlink(dir, link))
	require.Equal(t, service.SeverityOK, main.CheckInstancePermissions(link, gid).Severity)

	require.NoError(t, os.Chmod(key, 0o644)) //#nosec G302

	check := main.CheckInstancePermissions(dir, gid)
	require.Equal(t, service.SeverityError, check.Severity)
	require.Contains(t, check.Detail, "keys/operator.json is 0644, want 0600")

	require.NoError(t, os.Chmod(key, 0o600))
	require.NoError(t, os.Chmod(dir, 0o700)) //#nosec G302

	check = main.CheckInstancePermissions(dir, gid)
	require.Equal(t, service.SeverityWarning, check.Severity)
	require.Contains(t, check.Detail, "want 0750")

	require.NoError(t, os.Chmod(dir, 0o750)) //#nosec G302

	check = main.CheckInstancePermissions(dir, gid+1)
	require.Equal(t, service.SeverityWarning, check.Severity)
	require.Contains(t, check.Detail, "group sentinel-dvpn")
	require.Equal(t, service.SeverityOK, main.CheckInstancePermissions(dir, -1).Severity)
}

func TestCheckLeftoverKillSwitch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		runErr  error
		running bool
		want    service.Severity
	}{
		{name: "daemon unknown", runErr: errors.New("boom"), want: service.SeverityWarning},
		{name: "daemon running", running: true, want: service.SeverityOK},
		{name: "daemon stopped", want: service.SeverityWarning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			probe := main.NewKillSwitchProbeForTest(tc.running, false, tc.runErr, nil)
			checks := main.CheckLeftoverKillSwitch("", probe)
			require.Len(t, checks, 1)
			require.Equal(t, tc.want, checks[0].Severity)
			require.Equal(t, tc.want != service.SeverityError, checks[0].OK)
		})
	}
}

func TestCheckMetricsListen(t *testing.T) {
	t.Parallel()

	off := config.Metrics{}
	on := func(listen string) config.Metrics { return config.Metrics{Enabled: true, Listen: listen} }
	others := func(metrics config.Metrics) map[instance.Tag]config.Metrics {
		return map[instance.Tag]config.Metrics{"n1": metrics}
	}

	cases := []struct {
		detail     string
		name       string
		others     map[instance.Tag]config.Metrics
		own        config.Metrics
		unreadable []string
		want       service.Severity
	}{
		{name: "disabled here", others: others(on("127.0.0.1:9100")), own: off, want: service.SeverityOK},
		{
			detail: "instance n1 also serves metrics on 127.0.0.1:9100; give each",
			name:   "same address",
			others: others(on("127.0.0.1:9100")),
			own:    on("127.0.0.1:9100"),
			want:   service.SeverityError,
		},
		{
			name:   "wildcard overlaps",
			others: others(on("0.0.0.0:9100")),
			own:    on("127.0.0.1:9100"),
			want:   service.SeverityError,
		},
		{name: "other disabled", others: others(off), own: on("127.0.0.1:9100"), want: service.SeverityOK},
		{name: "other port", others: others(on("127.0.0.1:9101")), own: on("127.0.0.1:9100"), want: service.SeverityOK},
		{
			detail:     "n2: permission denied",
			name:       "unreadable instance",
			own:        on("127.0.0.1:9100"),
			unreadable: []string{"n2: permission denied"},
			want:       service.SeverityWarning,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			check := main.CheckMetricsListen(tc.own, tc.others, tc.unreadable)
			require.Equal(t, tc.want, check.Severity)
			require.Contains(t, check.Detail, tc.detail)
		})
	}
}

func TestCheckPersistedKillSwitch(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")

	cases := []struct {
		loaded  bool
		loadErr error
		name    string
		runErr  error
		running bool
		want    service.Severity
	}{
		{name: "daemon unknown", runErr: boom, want: service.SeverityWarning},
		{loadErr: boom, name: "kernel unreadable", running: true, want: service.SeverityWarning},
		{loaded: true, name: "running and loaded", running: true, want: service.SeverityOK},
		{name: "running not loaded", running: true, want: service.SeverityError},
		{loaded: true, name: "loaded from last run", want: service.SeverityOK},
		{name: "not loaded", want: service.SeverityOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			probe := main.NewKillSwitchProbeForTest(tc.running, tc.loaded, tc.runErr, tc.loadErr)
			checks := main.CheckPersistedKillSwitch(t.Context(), "", "", probe)
			require.Len(t, checks, 1)
			require.Equal(t, tc.want, checks[0].Severity)
			require.Equal(t, tc.want != service.SeverityError, checks[0].OK)
		})
	}
}

func TestDoctorChecks(t *testing.T) {
	t.Parallel()

	dump, err := os.ReadFile("testdata/sfltool-dumpbtm.txt")
	require.NoError(t, err)

	fakeExec := func(ctx context.Context, _ service.Command) ([]byte, error) { return dump, ctx.Err() }

	cases := []struct {
		goos string
		name string
		want []string
	}{
		{goos: "darwin", name: "launchd host", want: []string{"permissions", "background"}},
		{goos: "linux", name: "systemd host", want: []string{"permissions", "apparmor_wg"}},
		{goos: "windows", name: "windows host"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			plist := filepath.Join(root, "Library", "LaunchDaemons", "co.sentinel.sentinel-dvpnd.c0.plist")
			require.NoError(t, os.MkdirAll(filepath.Dir(plist), 0o750))
			require.NoError(t, os.WriteFile(plist, nil, 0o600))

			loaded := &config.Loaded{
				Dir:      t.TempDir(),
				Services: map[service.Name]any{wireguard.Name: nil},
				Tag:      "c0",
			}
			host := main.NewServiceHostForTest(fakeExec, nil, tc.goos, nil, root)

			var got []string
			for _, check := range main.DoctorChecks(t.Context(), loaded, host) {
				got = append(got, check.Name)
			}

			require.Equal(t, tc.want, got)
		})
	}
}

func TestNewDoctorCmd_RoleMismatch(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	_, err := runInitCmd(t, home, "--instance", "c0", "--owner", randomOwner(t), "--plain-key")
	require.NoError(t, err)

	var output bytes.Buffer

	cmd := main.NewRootCmd()
	cmd.SetOut(&output)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"doctor", "--home", home, "--instance", "n0"})

	require.ErrorIs(t, cmd.ExecuteContext(t.Context()), preflight.ErrPreflightFailed)
	require.Contains(t, output.String(), `"name":"role"`)
	require.Contains(t, output.String(), `"severity":"error"`)
}

func TestWriteChecks(t *testing.T) {
	t.Parallel()

	checks := []service.PreflightCheck{
		{Name: "rpc", OK: true, Severity: service.SeverityOK},
		{
			Detail:   "private peer addresses are allowed",
			Name:     "peer_allow_private",
			OK:       true,
			Severity: service.SeverityWarning,
		},
		{Detail: "a\nb\x1b", Name: "multiline", OK: true, Severity: service.SeverityOK},
	}

	var table, raw bytes.Buffer

	require.NoError(t, main.WriteChecks(&table, "table", checks))
	require.Regexp(t, `(?m)^warning\s+peer_allow_private\s+private peer addresses are allowed$`, table.String())
	require.Regexp(t, `(?m)^ok\s+multiline\s+a\\nb\\x1b$`, table.String())
	require.Len(t, bytes.Split(bytes.TrimSpace(table.Bytes()), []byte("\n")), 4)

	require.NoError(t, main.WriteChecks(&raw, "json", checks))
	require.Contains(t, raw.String(), `"severity":"warning"`)

	require.ErrorContains(t, main.WriteChecks(io.Discard, "yaml", checks), "output")
}
