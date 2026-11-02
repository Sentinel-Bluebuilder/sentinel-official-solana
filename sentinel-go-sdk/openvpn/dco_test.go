package openvpn_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/openvpn"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

type recordingExec struct {
	bounded  bool
	commands []service.Command
	err      error
}

func (r *recordingExec) run(ctx context.Context, c service.Command) ([]byte, error) {
	_, r.bounded = ctx.Deadline()
	r.commands = append(r.commands, c)

	return nil, r.err
}

func TestCheckDCO(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err     error
		name    string
		present bool
		want    string
	}{
		{name: "present", present: true},
		{name: "absent", want: "ovpn-dco driver not found; install OpenVPN 2.7.8 or later with its DCO driver"},
		{err: errors.New("access denied"), name: "error", want: "could not check the ovpn-dco driver: access denied"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checks := openvpn.CheckDCO(tc.present, tc.err)

			if tc.want == "" {
				require.Empty(t, checks)

				return
			}

			require.Equal(t, []service.PreflightCheck{
				service.NewPreflightCheck("service_driver", service.SeverityWarning, tc.want),
			}, checks)
		})
	}
}

func TestCheckVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		out  string
		want string
	}{
		{name: "current", out: "OpenVPN 2.7.8 x86_64-w64-mingw32 [SSL (OpenSSL)] [DCO]\n"},
		{name: "newer", out: "OpenVPN 2.10.0 x86_64\n"},
		{name: "old", out: "OpenVPN 2.6.12 x86_64-w64-mingw32\n", want: "OpenVPN 2.6.12 found; Windows needs 2.7.8 or later"},
		{name: "unreadable", out: "garbage", want: "could not read the OpenVPN version"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checks := openvpn.CheckVersion([]byte(tc.out))

			if tc.want == "" {
				require.Empty(t, checks)

				return
			}

			require.Equal(t, []service.PreflightCheck{
				service.NewPreflightCheck("service_version", service.SeverityWarning, tc.want),
			}, checks)
		})
	}
}

func TestTapctl_Create(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		stderr string
		want   string
	}{
		{name: "created"},
		{name: "exists", stderr: `Adapter "dvpn-c0" already exists (GUID {x}).`, want: "adapter dvpn-c0 is still registered"},
		{name: "in use", stderr: `Adapter name "dvpn-c0" is already in use.`, want: "adapter dvpn-c0 is still registered"},
		{name: "rename", stderr: `Renaming network adapter {x} to "dvpn-c0" failed`, want: "deleted it (OpenVPN issue #544)"},
		{name: "other", stderr: "Creating network adapter failed (error 0x5).", want: "tapctl create dvpn-c0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			recorder := &recordingExec{}
			if tc.stderr != "" {
				recorder.err = errors.New("running tapctl.exe: exit status 1: " + tc.stderr)
			}

			err := openvpn.NewTapctlForTest(recorder.run, nil, fakeLookPath).Create(t.Context(), adapterName)

			if tc.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.want)
			}

			require.Len(t, recorder.commands, 1)
			require.True(t, recorder.bounded)
			require.Equal(t, filepath.Join(binDir, "tapctl.exe"), recorder.commands[0].Path)
			require.Equal(t, binDir, recorder.commands[0].Dir)
			require.Equal(t, []string{"create", "--hwid", "ovpn-dco", "--name", adapterName}, recorder.commands[0].Args)
		})
	}
}

func TestTapctl_Delete(t *testing.T) {
	t.Parallel()

	cases := []struct {
		listed map[string]bool
		name   string
		want   string
	}{
		{name: "deleted"},
		{listed: map[string]bool{adapterName: true}, name: "still listed", want: "reboot"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			recorder := &recordingExec{}
			names := func() (map[string]bool, error) { return tc.listed, nil }
			err := openvpn.NewTapctlForTest(recorder.run, names, fakeLookPath).Delete(t.Context(), adapterName)

			if tc.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.want)
			}

			require.Len(t, recorder.commands, 1)
			require.Equal(t, []string{"delete", adapterName}, recorder.commands[0].Args)
		})
	}
}
