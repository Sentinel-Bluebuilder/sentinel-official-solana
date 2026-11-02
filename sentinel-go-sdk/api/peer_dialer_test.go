package api_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
)

func TestNewPeerDialer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		addr         string
		allowPrivate bool
		failing      []string
		name         string
		resolved     []string
		wantDialed   []string
		wantErr      error
		wantErrText  []string
		wantLookups  int
	}{
		{
			addr:        "peer.example:443",
			name:        "public host",
			resolved:    []string{"8.8.8.8"},
			wantDialed:  []string{"8.8.8.8:443"},
			wantLookups: 1,
		},
		{
			addr:        "peer.example:443",
			name:        "no addresses",
			wantErr:     api.ErrMissingPeerAddrs,
			wantLookups: 1,
		},
		{
			addr:       "[::ffff:8.8.8.8]:443",
			name:       "mapped public literal skips lookup",
			wantDialed: []string{"8.8.8.8:443"},
		},
		{
			addr:        "peer.example:443",
			name:        "private only",
			resolved:    []string{"10.0.0.1"},
			wantErr:     api.ErrAddrNotPublic,
			wantLookups: 1,
		},
		{
			addr:        "peer.example:443",
			name:        "mixed",
			resolved:    []string{"8.8.8.8", "10.0.0.1"},
			wantErr:     api.ErrAddrNotPublic,
			wantLookups: 1,
		},
		{
			addr:    "[::ffff:10.0.0.1]:443",
			name:    "mapped private literal skips lookup",
			wantErr: api.ErrAddrNotPublic,
		},
		{
			addr:         "peer.example:443",
			allowPrivate: true,
			name:         "allow private",
			resolved:     []string{"10.0.0.1"},
			wantDialed:   []string{"10.0.0.1:443"},
			wantLookups:  1,
		},
		{
			addr:         "peer.example:443",
			allowPrivate: true,
			failing:      []string{"10.0.0.1:443"},
			name:         "fallback in order",
			resolved:     []string{"10.0.0.2", "10.0.0.1", "10.0.0.1"},
			wantDialed:   []string{"10.0.0.1:443", "10.0.0.2:443"},
			wantLookups:  1,
		},
		{
			addr:        "peer.example:443",
			failing:     []string{"8.8.8.8:443", "1.1.1.1:443"},
			name:        "all dials fail",
			resolved:    []string{"8.8.8.8", "1.1.1.1"},
			wantDialed:  []string{"1.1.1.1:443", "8.8.8.8:443"},
			wantErrText: []string{"dial failed 1.1.1.1:443", "dial failed 8.8.8.8:443"},
			wantLookups: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var (
				lookups int
				dialed  []string
			)

			fakeLookup := func(context.Context, string, string) ([]netip.Addr, error) {
				lookups++

				var addrs []netip.Addr
				for _, s := range tc.resolved {
					addrs = append(addrs, netip.MustParseAddr(s))
				}

				return addrs, nil
			}

			clientConn, _ := net.Pipe()

			t.Cleanup(func() { _ = clientConn.Close() })

			recordingDial := func(_ context.Context, _, addr string) (net.Conn, error) {
				dialed = append(dialed, addr)

				if slices.Contains(tc.failing, addr) {
					return nil, errors.New("dial failed " + addr)
				}

				return clientConn, nil
			}

			dial := api.NewPeerDialerForTest(fakeLookup, tc.allowPrivate, recordingDial)
			conn, err := dial(t.Context(), "tcp", tc.addr)

			require.Equal(t, tc.wantLookups, lookups)
			require.Equal(t, tc.wantDialed, dialed)

			if len(tc.wantErrText) > 0 {
				for _, text := range tc.wantErrText {
					require.ErrorContains(t, err, text)
				}

				return
			}

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Nil(t, conn)

				return
			}

			require.NoError(t, err)
			require.Equal(t, clientConn, conn)
		})
	}
}
