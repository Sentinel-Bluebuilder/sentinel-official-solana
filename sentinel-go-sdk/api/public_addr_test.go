package api_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
)

func TestValidatePublicAddr(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		addr string
		ok   bool
	}{
		{"public ipv4", "8.8.8.8", true},
		{"public ipv4 second", "1.1.1.1", true},
		{"public ipv6", "2606:4700::1111", true},
		{"ipv4-mapped public", "::ffff:8.8.8.8", true},
		{"loopback ipv4", "127.0.0.1", false},
		{"private 10/8", "10.1.2.3", false},
		{"private 172.16/12", "172.16.0.1", false},
		{"private 192.168/16", "192.168.1.1", false},
		{"link-local ipv4", "169.254.1.1", false},
		{"multicast ipv4", "224.0.0.1", false},
		{"unspecified ipv4", "0.0.0.0", false},
		{"0/8", "0.1.2.3", false},
		{"cgnat 100.64/10", "100.64.0.1", false},
		{"192.0.0/24", "192.0.0.1", false},
		{"test-net-1", "192.0.2.1", false},
		{"benchmark 198.18/15", "198.18.0.1", false},
		{"test-net-2", "198.51.100.1", false},
		{"test-net-3", "203.0.113.1", false},
		{"reserved 240/4", "240.0.0.1", false},
		{"loopback ipv6", "::1", false},
		{"unspecified ipv6", "::", false},
		{"link-local ipv6", "fe80::1", false},
		{"unique local fc00::/7", "fc00::1", false},
		{"multicast ipv6", "ff02::1", false},
		{"documentation 2001:db8::/32", "2001:db8::1", false},
		{"ipv4-mapped private", "::ffff:10.0.0.1", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			addr := netip.MustParseAddr(tc.addr)

			err := api.ValidatePublicAddr(addr)
			if tc.ok {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, api.ErrAddrNotPublic)
			require.ErrorContains(t, err, addr.String())
		})
	}
}
