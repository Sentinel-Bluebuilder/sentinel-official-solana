package service_test

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

func TestRenderRuleset(t *testing.T) {
	t.Parallel()

	prefixes := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24"), netip.MustParsePrefix("fd00::/64")}

	out := service.RenderRuleset("sentinel-dvpn-n0", "dvpn-n0", "eth0", prefixes)

	require.True(t, strings.HasPrefix(out, "add table inet sentinel-dvpn-n0\ndelete table inet sentinel-dvpn-n0\n"))
	require.Contains(t, out, "table inet sentinel-dvpn-n0 {")
	require.Contains(t, out, "type filter hook forward priority 0; policy accept;")
	require.Contains(t, out, `iifname "dvpn-n0" accept`)
	require.Contains(t, out, `oifname "dvpn-n0" ct state established,related accept`)
	require.Contains(t, out, "type nat hook postrouting priority srcnat; policy accept;")
	require.Contains(t, out, `ip saddr 10.0.0.0/24 oifname "eth0" masquerade`)
	require.Contains(t, out, `ip6 saddr fd00::/64 oifname "eth0" masquerade`)
}
