//go:build linux

package hysteria2_test

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/hysteria2"
)

// fakeFirewall records every command and answers the detection probes from a script, so the test
// drives the backend selection without a real firewall.
type fakeFirewall struct {
	calls []string
	// detect maps a probe ("firewall-cmd --state", "ufw status", "nft --version") to its output;
	// a probe absent from the map errors (as a missing tool would).
	detect map[string]string
}

func (f *fakeFirewall) run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := name
	for _, a := range args {
		line += " " + a
	}

	// Detection probes answer from the script; everything else is an action we record as succeeding.
	switch {
	case name == "firewall-cmd" && len(args) == 1 && args[0] == "--state":
		if out, ok := f.detect["firewall-cmd --state"]; ok {
			return []byte(out), nil
		}

		return nil, errors.New("not running")
	case name == "ufw" && len(args) == 1 && args[0] == "status":
		if out, ok := f.detect["ufw status"]; ok {
			return []byte(out), nil
		}

		return nil, errors.New("inactive")
	case name == "nft" && len(args) == 1 && args[0] == "--version":
		if _, ok := f.detect["nft --version"]; ok {
			return []byte("nftables v1.0"), nil
		}

		return nil, errors.New("no nft")
	}

	f.calls = append(f.calls, line)

	return nil, nil
}

func TestOpenCloseInboundUDP(t *testing.T) {
	t.Parallel()

	const port uint16 = 51822

	cases := []struct {
		name        string
		detect      map[string]string
		wantBackend string
		wantOpen    string
		wantClose   string
	}{
		{
			name:        "firewalld running",
			detect:      map[string]string{"firewall-cmd --state": "running\n"},
			wantBackend: "firewalld",
			wantOpen:    "firewall-cmd --add-port=51822/udp",
			wantClose:   "firewall-cmd --remove-port=51822/udp",
		},
		{
			name:        "ufw active",
			detect:      map[string]string{"ufw status": "Status: active\n"},
			wantBackend: "ufw",
			wantOpen:    "ufw allow 51822/udp",
			wantClose:   "ufw delete allow 51822/udp",
		},
		{
			name:        "nft fallback",
			detect:      map[string]string{"nft --version": "yes"},
			wantBackend: "nft",
			wantOpen:    "nft add rule inet sentinel-dvpn-hy2-51822 input udp dport 51822 accept",
			wantClose:   "nft delete table inet sentinel-dvpn-hy2-51822",
		},
		{
			name:        "no firewall",
			detect:      map[string]string{},
			wantBackend: "",
			wantOpen:    "",
			wantClose:   "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fw := &fakeFirewall{detect: tc.detect}

			backend, err := hysteria2.OpenInboundUDPForTest(context.Background(), fw.run, port, zerolog.Nop())
			require.NoError(t, err)
			require.Equal(t, tc.wantBackend, backend)

			if tc.wantOpen == "" {
				require.Empty(t, fw.calls, "no managed firewall: must issue no open command")
			} else {
				require.Contains(t, fw.calls, tc.wantOpen)
			}

			fw.calls = nil

			require.NoError(t, hysteria2.CloseInboundUDPForTest(context.Background(), fw.run, backend, port, zerolog.Nop()))

			if tc.wantClose == "" {
				require.Empty(t, fw.calls, "nothing opened: must issue no close command")
			} else {
				require.Contains(t, fw.calls, tc.wantClose)
			}
		})
	}
}
