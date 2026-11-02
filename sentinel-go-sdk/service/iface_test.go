package service_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
)

func TestParseNetstat(t *testing.T) {
	t.Parallel()

	out, err := os.ReadFile("testdata/netstat_ibn.txt")
	require.NoError(t, err)

	cases := []struct {
		iface  string
		wantRx uint64
		wantTx uint64
	}{
		{iface: "en0", wantRx: 1500000000, wantTx: 250000000},
		{iface: "utun3", wantRx: 654321, wantTx: 123456},
	}
	for _, tc := range cases {
		t.Run(tc.iface, func(t *testing.T) {
			t.Parallel()

			stats, err := service.ParseNetstat(out, tc.iface)
			require.NoError(t, err)
			require.Equal(t, tc.wantRx, stats.RxBytes)
			require.Equal(t, tc.wantTx, stats.TxBytes)
		})
	}
}

func TestParseNetstat_MalformedStats(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		out       []byte
		wantError string
	}{
		{
			name:      "receive bytes",
			out:       []byte("en0 1500 <Link#11> aa:bb:cc:dd:ee:ff 1 0 invalid 1 0 2 0\n"),
			wantError: "parsing en0 rx bytes",
		},
		{
			name:      "transmit bytes",
			out:       []byte("en0 1500 <Link#11> aa:bb:cc:dd:ee:ff 1 0 2 1 0 invalid 0\n"),
			wantError: "parsing en0 tx bytes",
		},
		{
			name:      "scanner token limit",
			out:       bytes.Repeat([]byte("x"), 64*1024+1),
			wantError: "scanning netstat output",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := service.ParseNetstat(tc.out, "en0")
			require.ErrorContains(t, err, tc.wantError)
		})
	}
}

func TestParseNetstat_Missing(t *testing.T) {
	t.Parallel()

	out, err := os.ReadFile("testdata/netstat_ibn.txt")
	require.NoError(t, err)

	_, err = service.ParseNetstat(out, "utun9")
	require.ErrorIs(t, err, service.ErrPeerNotFound)
}

func TestProbeKernelSupport(t *testing.T) {
	t.Parallel()

	probe := service.Command{
		Args: []string{"--net", "ip", "link", "add", "dev", "dvpn-probe", "type", "wireguard"},
		Path: "unshare",
	}

	cases := []struct {
		goos    string
		name    string
		runErr  error
		want    bool
		wantErr bool
	}{
		{goos: "linux", name: "supported", want: true},
		{
			goos:   "linux",
			name:   "missing module",
			runErr: errors.New("running unshare: exit status 2: Error: Unknown device type."),
		},
		{
			goos:   "linux",
			name:   "unsupported operation",
			runErr: errors.New("running unshare: exit status 2: RTNETLINK answers: Operation not supported"),
		},
		{
			goos:    "linux",
			name:    "unshare denied",
			runErr:  errors.New("running unshare: exit status 1: unshare failed: Operation not permitted"),
			wantErr: true,
		},
		{goos: "darwin", name: "not linux"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls []service.Command

			recordingExec := func(_ context.Context, command service.Command) ([]byte, error) {
				calls = append(calls, command)

				return nil, tc.runErr
			}

			got, err := service.ProbeKernelSupport(t.Context(), "wireguard", tc.goos, recordingExec)
			if tc.wantErr {
				require.ErrorIs(t, err, tc.runErr)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tc.want, got)

			if tc.goos == "linux" {
				require.Equal(t, []service.Command{probe}, calls)
			} else {
				require.Empty(t, calls)
			}
		})
	}
}

func TestSelectNewInterface(t *testing.T) {
	t.Parallel()

	cases := []struct {
		after map[string]bool
		err   error
		goos  string
		name  string
		want  string
	}{
		{
			after: map[string]bool{"en1": true, "utun4": true},
			goos:  "darwin",
			name:  "physical and tunnel",
			want:  "utun4",
		},
		{
			after: map[string]bool{"en1": true, "utun": true, "utunx": true, "utun-1": true},
			err:   service.ErrNewInterfaceNotFound,
			goos:  "darwin",
			name:  "no tunnel",
		},
		{
			after: map[string]bool{"utun4": true, "utun5": true},
			err:   service.ErrInterfaceAmbiguous,
			goos:  "darwin",
			name:  "multiple tunnels",
		},
		{
			after: map[string]bool{"en1": true, "utun4": true},
			goos:  "linux",
			name:  "linux compatibility",
			want:  "en1",
		},
		{
			after: map[string]bool{"en1": true, "utun4": true},
			goos:  goosWindows,
			name:  "windows compatibility",
			want:  "en1",
		},
		{
			after: map[string]bool{"utun0": true},
			err:   service.ErrNewInterfaceNotFound,
			goos:  "darwin",
			name:  "existing tunnel",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := service.SelectNewInterface(map[string]bool{"utun0": true}, tc.after, tc.goos)
			require.ErrorIs(t, err, tc.err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestValidateInterfaceName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err   error
		iface string
		name  string
	}{
		{iface: "dvpn-n0", name: "valid"},
		{err: validate.ErrInvalidConfig, iface: "a b", name: "space"},
		{err: validate.ErrInvalidConfig, iface: "abcdefghijklmnop", name: "sixteen characters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := service.ValidateInterfaceName(tc.iface)
			if tc.err == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, tc.err)
		})
	}
}
