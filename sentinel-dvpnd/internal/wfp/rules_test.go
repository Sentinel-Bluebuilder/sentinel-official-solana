package wfp_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/wfp"
)

func TestRules(t *testing.T) {
	t.Parallel()

	nodeAddrs := []netip.Addr{netip.MustParseAddr("203.0.113.1"), netip.MustParseAddr("2001:db8::9")}
	rpcAndWSAddrs := []netip.AddrPort{netip.MustParseAddrPort("10.99.0.1:8899")}

	want := []wfp.Rule{
		{Action: wfp.ActionBlock, Layer: wfp.LayerConnectV4, Name: "block all"},
		{Action: wfp.ActionPermit,
			Conditions: []wfp.Condition{{Field: wfp.FieldFlags, Flags: 1}},
			Layer:      wfp.LayerConnectV4,
			Name:       "loopback",
			Weight:     15},
		{Action: wfp.ActionPermit,
			Conditions: []wfp.Condition{{Field: wfp.FieldLocalInterface, InterfaceLUID: 77}},
			Layer:      wfp.LayerConnectV4,
			Name:       "tunnel",
			Weight:     15},
		{Action: wfp.ActionBlock, Layer: wfp.LayerConnectV6, Name: "block all"},
		{Action: wfp.ActionPermit,
			Conditions: []wfp.Condition{{Field: wfp.FieldFlags, Flags: 1}},
			Layer:      wfp.LayerConnectV6,
			Name:       "loopback",
			Weight:     15},
		{Action: wfp.ActionPermit,
			Conditions: []wfp.Condition{{Field: wfp.FieldLocalInterface, InterfaceLUID: 77}},
			Layer:      wfp.LayerConnectV6,
			Name:       "tunnel",
			Weight:     15},
		{Action: wfp.ActionPermit,
			Conditions: []wfp.Condition{{Field: wfp.FieldRemoteAddress, Prefix: netip.MustParsePrefix("203.0.113.1/32")}},
			Layer:      wfp.LayerConnectV4,
			Name:       "node 203.0.113.1",
			Weight:     15},
		{Action: wfp.ActionPermit,
			Conditions: []wfp.Condition{{Field: wfp.FieldRemoteAddress, Prefix: netip.MustParsePrefix("2001:db8::9/128")}},
			Layer:      wfp.LayerConnectV6,
			Name:       "node 2001:db8::9",
			Weight:     15},
		{Action: wfp.ActionPermit, Conditions: []wfp.Condition{
			{Field: wfp.FieldProtocol, Protocol: 6},
			{Field: wfp.FieldRemoteAddress, Prefix: netip.MustParsePrefix("10.99.0.1/32")},
			{Field: wfp.FieldRemotePort, Port: 8899},
		}, Layer: wfp.LayerConnectV4, Name: "rpc 10.99.0.1:8899", Weight: 15},
		{Action: wfp.ActionPermit,
			Conditions: []wfp.Condition{{Field: wfp.FieldProtocol, Protocol: 17},
				{Field: wfp.FieldRemotePort, Port: 67}},
			Layer:  wfp.LayerConnectV4,
			Name:   "dhcp 67",
			Weight: 15},
		{Action: wfp.ActionPermit,
			Conditions: []wfp.Condition{{Field: wfp.FieldProtocol, Protocol: 17},
				{Field: wfp.FieldRemotePort, Port: 68}},
			Layer:  wfp.LayerConnectV4,
			Name:   "dhcp 68",
			Weight: 15},
		{Action: wfp.ActionPermit,
			Conditions: []wfp.Condition{{Field: wfp.FieldProtocol, Protocol: 17},
				{Field: wfp.FieldRemotePort, Port: 546}},
			Layer:  wfp.LayerConnectV6,
			Name:   "dhcp 546",
			Weight: 15},
		{Action: wfp.ActionPermit,
			Conditions: []wfp.Condition{{Field: wfp.FieldProtocol, Protocol: 17},
				{Field: wfp.FieldRemotePort, Port: 547}},
			Layer:  wfp.LayerConnectV6,
			Name:   "dhcp 547",
			Weight: 15},
		{Action: wfp.ActionPermit,
			Conditions: []wfp.Condition{{Field: wfp.FieldRemoteAddress, Prefix: netip.MustParsePrefix("169.254.0.0/16")}},
			Layer:      wfp.LayerConnectV4,
			Name:       "link-local",
			Weight:     15},
		{Action: wfp.ActionPermit,
			Conditions: []wfp.Condition{{Field: wfp.FieldRemoteAddress, Prefix: netip.MustParsePrefix("fe80::/10")}},
			Layer:      wfp.LayerConnectV6,
			Name:       "link-local",
			Weight:     15},
		{Action: wfp.ActionPermit,
			Conditions: []wfp.Condition{{Field: wfp.FieldRemoteAddress, Prefix: netip.MustParsePrefix("10.0.0.0/8")}},
			Layer:      wfp.LayerConnectV4,
			Name:       "lan 10.0.0.0/8",
			Weight:     15},
		{Action: wfp.ActionPermit,
			Conditions: []wfp.Condition{{Field: wfp.FieldRemoteAddress, Prefix: netip.MustParsePrefix("172.16.0.0/12")}},
			Layer:      wfp.LayerConnectV4,
			Name:       "lan 172.16.0.0/12",
			Weight:     15},
		{Action: wfp.ActionPermit,
			Conditions: []wfp.Condition{{Field: wfp.FieldRemoteAddress, Prefix: netip.MustParsePrefix("192.168.0.0/16")}},
			Layer:      wfp.LayerConnectV4,
			Name:       "lan 192.168.0.0/16",
			Weight:     15},
	}
	require.Equal(t, want, wfp.NewRules(77, nodeAddrs, rpcAndWSAddrs, true))
}

func TestRules_MappedAddresses(t *testing.T) {
	t.Parallel()

	mapped := netip.MustParseAddr("::ffff:203.0.113.1")
	plain := mapped.Unmap()
	require.Equal(t,
		wfp.NewRules(0, []netip.Addr{plain}, []netip.AddrPort{netip.AddrPortFrom(plain, 443)}, false),
		wfp.NewRules(0, []netip.Addr{mapped}, []netip.AddrPort{netip.AddrPortFrom(mapped, 443)}, false),
	)
}

func TestRules_NoInterface(t *testing.T) {
	t.Parallel()

	rules := wfp.NewRules(0, nil, nil, false)

	for _, r := range rules {
		require.NotEqual(t, "tunnel", r.Name)

		for _, c := range r.Conditions {
			require.NotEqual(t, wfp.FieldLocalInterface, c.Field)
		}
	}

	var blocks int

	for _, r := range rules {
		if r.Action == wfp.ActionBlock {
			blocks++
		}
	}

	require.Equal(t, 2, blocks)
}
