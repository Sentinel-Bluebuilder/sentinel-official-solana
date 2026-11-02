package main_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	main "github.com/sentinel-official/sentinel-dvpnd/cmd/sentinel-dvpnd"
	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-go-sdk/v3/amneziawg"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/wireguard"
)

const reinstallHint = "sentinel-dvpnd service install --instance "

func TestCheckDropIn(t *testing.T) {
	t.Parallel()

	loaded := &config.Loaded{Services: map[service.Name]any{amneziawg.Name: nil}, Tag: "n1"}

	cases := []struct {
		dropIn    []service.Name
		kernel    bool
		kernelErr error
		name      string
		want      service.Severity
	}{
		{name: "missing", want: service.SeverityError},
		{dropIn: []service.Name{amneziawg.Name}, name: "current", want: service.SeverityOK},
		{dropIn: []service.Name{amneziawg.Name}, kernel: true, name: "stale", want: service.SeverityWarning},
		{kernel: true, name: "none", want: service.SeverityOK},
		{dropIn: []service.Name{wireguard.Name}, name: "out of date", want: service.SeverityError},
		{kernelErr: errors.New("operation not permitted"), name: "kernel probe fails", want: service.SeverityWarning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, "etc", "systemd", "system"), 0o750))
			require.NoError(t, main.WriteUserspaceDropIn(root, loaded.Tag, tc.dropIn))

			fakeKernelSupport := func(context.Context, string) (bool, error) { return tc.kernel, tc.kernelErr }
			host := main.NewServiceHostForTest(nil, nil, "linux", fakeKernelSupport, root)
			check := main.CheckDropIn(t.Context(), host, loaded)
			require.Equal(t, tc.want, check.Severity)

			if tc.want != service.SeverityOK && tc.kernelErr == nil {
				require.Contains(t, check.Detail, reinstallHint+"n1")
			}
		})
	}
}

func TestCheckServiceInstall(t *testing.T) {
	t.Parallel()

	loaded := &config.Loaded{Services: map[service.Name]any{wireguard.Name: nil}, Tag: "n0"}
	fakeKernelSupport := func(context.Context, string) (bool, error) { return true, nil }
	root := t.TempDir()
	host := main.NewServiceHostForTest(nil, nil, "linux", fakeKernelSupport, root)

	require.Empty(t, main.CheckServiceInstall(t.Context(), host, loaded))

	wants := filepath.Join(root, "etc", "systemd", "system", "multi-user.target.wants")
	require.NoError(t, os.MkdirAll(wants, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(wants, "sentinel-dvpnd@n0.service"), nil, 0o600))

	checks := main.CheckServiceInstall(t.Context(), host, loaded)
	require.Len(t, checks, 2)
	require.Equal(t, "tool_dirs", checks[0].Name)
	require.Equal(t, service.SeverityError, checks[0].Severity)
	require.Equal(t, "userspace_drop_in", checks[1].Name)
	require.Equal(t, service.SeverityOK, checks[1].Severity)
}

func TestCheckServiceInstall_UninspectableRoot(t *testing.T) {
	t.Parallel()

	loaded := &config.Loaded{Services: map[service.Name]any{wireguard.Name: nil}, Tag: "n0"}
	host := main.NewServiceHostForTest(nil, nil, "linux", nil, uninspectableRootFixture(t))

	checks := main.CheckServiceInstall(t.Context(), host, loaded)
	require.Len(t, checks, 1)
	require.Equal(t, "installed", checks[0].Name)
	require.Equal(t, service.SeverityWarning, checks[0].Severity)
}

func TestCheckToolDirs(t *testing.T) {
	t.Parallel()

	loaded := &config.Loaded{Services: map[service.Name]any{wireguard.Name: nil}, Tag: "n0"}
	dir := main.InstanceToolDirs(loaded)[0]
	root := t.TempDir()
	host := main.NewServiceHostForTest(nil, nil, "linux", nil, root)

	check := main.CheckToolDirs(host, loaded)
	require.Equal(t, service.SeverityError, check.Severity)
	require.Contains(t, check.Detail, dir)
	require.Contains(t, check.Detail, reinstallHint+"n0")

	require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o700))
	require.Equal(t, service.SeverityOK, main.CheckToolDirs(host, loaded).Severity)
}

func TestCheckToolDirs_UninspectableRoot(t *testing.T) {
	t.Parallel()

	loaded := &config.Loaded{Services: map[service.Name]any{wireguard.Name: nil}, Tag: "n0"}
	host := main.NewServiceHostForTest(nil, nil, "linux", nil, uninspectableRootFixture(t))

	check := main.CheckToolDirs(host, loaded)
	require.Equal(t, service.SeverityWarning, check.Severity)
	require.Contains(t, check.Detail, "cannot inspect "+main.InstanceToolDirs(loaded)[0])
}

func TestCheckWireGuardConfinement(t *testing.T) {
	t.Parallel()

	dropIn := "[Service]\nRuntimeDirectory=" + wireguard.Name.String() + "\nRuntimeDirectoryPreserve=yes\n"
	dropInName, kernelName, runName := "userspace_drop_in_wireguard", "kernel_wireguard", "run_wireguard"

	cases := []struct {
		dropIn string
		kernel bool
		name   string
		runDir bool
		want   map[string]string
	}{
		{kernel: true, name: "kernel, clean host"},
		{name: "no kernel module", want: map[string]string{kernelName: "confines wg; load the WireGuard kernel module"}},
		{
			dropIn: dropIn,
			name:   "wireguard drop-in",
			want:   map[string]string{dropInName: "service install --instance n0", kernelName: "modprobe wireguard"},
		},
		{kernel: true, name: "run folder on kernel host", runDir: true, want: map[string]string{runName: "/run/wireguard"}},
		{name: "run folder without kernel module", runDir: true, want: map[string]string{kernelName: "modprobe wireguard"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			run := filepath.Join(root, "run", "wireguard")

			if tc.runDir {
				require.NoError(t, os.MkdirAll(run, 0o750))
			}

			checks := main.CheckWireGuardConfinement(root, "n0", tc.kernel, tc.dropIn)
			require.Len(t, checks, len(tc.want))

			for _, check := range checks {
				require.Contains(t, tc.want, check.Name)
				require.Equal(t, service.SeverityError, check.Severity)
				require.Contains(t, check.Detail, tc.want[check.Name])
			}

			if tc.runDir {
				require.DirExists(t, run)
			}
		})
	}
}

func TestInspectConfinement(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "etc", "systemd", "system"), 0o750))
	require.NoError(t, main.WriteUserspaceDropIn(root, "n0", []service.Name{wireguard.Name}))

	failingKernelSupport := func(context.Context, string) (bool, error) {
		return false, errors.New("operation not permitted")
	}
	host := main.NewServiceHostForTest(nil, nil, "linux", failingKernelSupport, root)
	checks := main.InspectConfinement(t.Context(), host, "n0")
	require.Len(t, checks, 1)
	require.Equal(t, "kernel_wireguard", checks[0].Name)
	require.Equal(t, service.SeverityWarning, checks[0].Severity)

	fakeKernelSupport := func(context.Context, string) (bool, error) { return true, nil }
	host = main.NewServiceHostForTest(nil, nil, "linux", fakeKernelSupport, root)
	checks = main.InspectConfinement(t.Context(), host, "n0")
	require.Len(t, checks, 1)
	require.Equal(t, "userspace_drop_in_wireguard", checks[0].Name)
	require.Equal(t, service.SeverityError, checks[0].Severity)
}

func TestServiceHost_WGEnforced(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		probed   bool
		profiles string
		runErr   error
		want     bool
		wantErr  error
	}{
		{name: "securityfs enforce", profiles: "wg (enforce)\nwg-quick (enforce)\n", want: true},
		{name: "securityfs complain", profiles: "wg (complain)\n"},
		{name: "securityfs without wg", profiles: "nvidia_modprobe (enforce)\n"},
		{
			name:   "hidden securityfs, probe denied",
			probed: true,
			runErr: errors.New("running wg: exit status 1: fopen: Permission denied"),
			want:   true,
		},
		{
			name:   "hidden securityfs, probe reaches the kernel",
			probed: true,
			runErr: errors.New("running wg: exit status 1: Unable to modify interface: Operation not supported"),
		},
		{
			name:    "hidden securityfs, wg missing",
			probed:  true,
			runErr:  fmt.Errorf("running wg: %w", exec.ErrNotFound),
			wantErr: exec.ErrNotFound,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			path := filepath.Join(root, "sys", "kernel", "security", "apparmor", "profiles")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
			require.NoError(t, os.WriteFile(path, []byte(tc.profiles), 0o600))

			var file string

			fakeExec := func(_ context.Context, c service.Command) ([]byte, error) {
				require.Equal(t, "wg", c.Path)
				require.Equal(t, []string{"setconf", "lo"}, c.Args[:2])
				require.FileExists(t, c.Args[2])

				file = c.Args[2]

				return nil, tc.runErr
			}

			host := main.NewServiceHostForTest(fakeExec, nil, "linux", nil, root)
			got, err := main.ServiceHostWGEnforced(host, t.Context())
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.probed, file != "")

			if tc.probed {
				require.NotEqual(t, "/etc/wireguard", filepath.Dir(file))
				require.NoFileExists(t, file)
			}

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func uninspectableRootFixture(t *testing.T) string {
	t.Helper()

	if filepath.Separator != '/' {
		t.Skip("a path through a regular file is reported as missing here")
	}

	root := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(root, nil, 0o600))

	return root
}
