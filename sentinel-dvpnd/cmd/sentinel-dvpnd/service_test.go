package main_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	main "github.com/sentinel-official/sentinel-dvpnd/cmd/sentinel-dvpnd"
	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
)

type recordingServiceManager struct {
	main.ServiceManager

	installed  map[instance.Tag]bool
	installs   []instance.Tag
	prunes     int
	uninstalls []instance.Tag
}

func (m *recordingServiceManager) Install(_ context.Context, loaded *config.Loaded) error {
	m.installs = append(m.installs, loaded.Tag)

	return nil
}

func (m *recordingServiceManager) Installed(_ context.Context, tag instance.Tag) (bool, error) {
	return m.installed[tag], nil
}

func (m *recordingServiceManager) Prune(context.Context) error {
	m.prunes++

	return nil
}

func (m *recordingServiceManager) Uninstall(_ context.Context, tag instance.Tag) error {
	m.uninstalls = append(m.uninstalls, tag)

	return nil
}

func TestCanonicalExecutable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	short, canonical := filepath.Join(dir, "dvpnd.exe"), filepath.Join(dir, "sentinel-dvpnd.exe")
	alone := filepath.Join(t.TempDir(), "dvpnd")

	for _, path := range []string{short, canonical} {
		require.NoError(t, os.WriteFile(path, nil, 0o600))
	}

	cases := []struct {
		name string
		path string
		want string
	}{
		{name: "short name beside the canonical binary", path: short, want: canonical},
		{name: "short name alone", path: alone, want: alone},
		{name: "canonical name", path: canonical, want: canonical},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, main.CanonicalExecutable(tc.path))
		})
	}
}

func TestInstallService(t *testing.T) {
	t.Setenv("SENTINEL_DVPND_RPC_URL", "http://127.0.0.1:8899")
	t.Setenv("SENTINEL_DVPND_RPC_WS_URL", "ws://127.0.0.1:8900")

	fake := &recordingServiceManager{}
	recorder := &recordingExec{}
	host := main.NewServiceHostForTest(recorder.run, nil, "linux", nil, t.TempDir())
	out := &bytes.Buffer{}
	require.ErrorIs(t, main.InstallService(t.Context(), host, fake, "c0", t.TempDir(), out), instance.ErrInstanceNotFound)
	require.Empty(t, fake.installs)
	require.Empty(t, recorder.commands)
	require.Empty(t, out.String())

	home := t.TempDir()
	_, err := runInitCmd(t, home, "--instance", "c0", "--owner", randomOwner(t), "--plain-key")
	require.NoError(t, err)
	require.NoError(t, os.Chmod(home, 0o700)) //#nosec G302 -- proves install re-applies the instance folder mode
	require.NoError(t, main.InstallService(t.Context(), host, fake, "c0", home, out))

	if runtime.GOOS != goosWindows {
		info, err := os.Stat(home)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o750), info.Mode().Perm())
	}

	require.Equal(t, []instance.Tag{"c0"}, fake.installs)
	require.Equal(t, []string{"getent group sentinel-dvpn"}, recorder.commands)
	require.Equal(t, strings.Join(main.ServiceHints("linux", "c0"), "\n")+"\n", out.String())
}

func TestServiceHints(t *testing.T) {
	t.Parallel()

	const (
		grant = "grant a user access (effective at next login): "
		plist = "/Library/LaunchDaemons/co.sentinel.sentinel-dvpnd.c0.plist"
	)

	cases := []struct {
		goos string
		name string
		tag  instance.Tag
		want []string
	}{
		{
			goos: "darwin",
			name: "launchd",
			tag:  "c0",
			want: []string{
				"installed co.sentinel.sentinel-dvpnd.c0 (not started)",
				"start: launchctl bootstrap system " + plist + " (when not loaded), " +
					"then launchctl kickstart system/co.sentinel.sentinel-dvpnd.c0",
				"stop: launchctl bootout system/co.sentinel.sentinel-dvpnd.c0",
				grant + "dseditgroup -o edit -a <user> -t user sentinel-dvpn",
			},
		},
		{
			goos: "linux",
			name: "systemd",
			tag:  "n0",
			want: []string{
				"installed sentinel-dvpnd@n0 (not started)",
				"start: systemctl start sentinel-dvpnd@n0",
				"stop: systemctl stop sentinel-dvpnd@n0.socket sentinel-dvpnd@n0.service",
				grant + "usermod -aG sentinel-dvpn <user>",
			},
		},
		{
			goos: "windows",
			name: "service control manager",
			tag:  "c0",
			want: []string{
				"installed sentinel-dvpnd$c0 (not started)",
				"start: sc.exe start 'sentinel-dvpnd$c0'",
				"stop: sc.exe stop 'sentinel-dvpnd$c0'",
				grant + "net localgroup sentinel-dvpn <user> /add",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, main.ServiceHints(tc.goos, tc.tag))
		})
	}
}

func TestUninstallService_LastInstance(t *testing.T) {
	t.Parallel()

	cases := []struct {
		installed bool
		name      string
		prunes    int
		want      []string
	}{
		{installed: true, name: "other installed"},
		{
			name:   "last instance",
			prunes: 1,
			want:   []string{"getent group sentinel-dvpn", "groupdel sentinel-dvpn"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			for _, tag := range []string{"c0", "c1"} {
				require.NoError(t, os.MkdirAll(filepath.Join(root, "var", "lib", "sentinel-dvpn", tag), 0o750))
			}

			fake := &recordingServiceManager{installed: map[instance.Tag]bool{"c0": true, "c1": tc.installed}}
			recorder := &recordingExec{}
			host := main.NewServiceHostForTest(recorder.run, nil, "linux", nil, root)

			require.NoError(t, main.UninstallService(t.Context(), host, fake, "c0"))
			require.Equal(t, []instance.Tag{"c0"}, fake.uninstalls)
			require.Equal(t, tc.prunes, fake.prunes)
			require.Equal(t, tc.want, recorder.commands)
		})
	}
}
