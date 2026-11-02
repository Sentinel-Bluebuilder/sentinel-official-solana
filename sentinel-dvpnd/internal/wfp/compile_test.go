package wfp_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/wfp"
)

func TestCompile(t *testing.T) {
	t.Parallel()

	views, _, err := wfp.CompileForTest("sentinel-dvpn-c0", wfp.NewRules(77, nil, nil, false))
	require.NoError(t, err)
	require.Len(t, views, 12)

	for _, v := range views {
		require.Nil(t, v.Provider)
		require.Equal(t, wfp.DeriveSublayerKey("sentinel-dvpn-c0"), v.Sublayer)
		require.EqualValues(t, 1, v.Flags)
		require.EqualValues(t, 1, v.WeightKind)

		if v.Action == 0x1001 {
			require.Empty(t, v.Conditions)
			require.Zero(t, v.Weight)
		} else {
			require.EqualValues(t, 0x1002, v.Action)
			require.EqualValues(t, 15, v.Weight)
		}
	}

	require.Equal(t, wfp.LayerConnectV4GUID, views[0].Layer)
	require.Equal(t, wfp.LayerConnectV6GUID, views[3].Layer)
	require.EqualValues(t, 6, views[1].Conditions[0].Match)
	require.EqualValues(t, 77, views[2].Conditions[0].Indirect)
}

func TestCompile_Address(t *testing.T) {
	t.Parallel()

	cases := []struct {
		prefix string
		layer  wfp.Layer
		kind   uint32
		data   uint64
		mask   uint32
		bytes  [16]byte
		bits   uint8
	}{
		{prefix: "203.0.113.1/32", layer: wfp.LayerConnectV4, kind: 3, data: 0xcb007101},
		{prefix: "203.0.113.9/24", layer: wfp.LayerConnectV4, kind: 256, data: 0xcb007100, mask: 0xffffff00},
		{prefix: "0.0.0.0/0", layer: wfp.LayerConnectV4, kind: 256},
		{prefix: "2001:db8::9/128", layer: wfp.LayerConnectV6, kind: 11, bytes: netip.MustParseAddr("2001:db8::9").As16()},
		{prefix: "2001:db8::9/32",
			layer: wfp.LayerConnectV6,
			kind:  257,
			bytes: netip.MustParseAddr("2001:db8::").As16(),
			bits:  32},
	}
	for _, tc := range cases {
		t.Run(tc.prefix, func(t *testing.T) {
			t.Parallel()

			views, _, err := wfp.CompileForTest("sentinel-dvpn-c0", []wfp.Rule{{Action: wfp.ActionPermit,
				Layer:      tc.layer,
				Conditions: []wfp.Condition{{Field: wfp.FieldRemoteAddress, Prefix: netip.MustParsePrefix(tc.prefix)}},
			}})
			require.NoError(t, err)

			c := views[0].Conditions[0]
			require.Equal(t, tc.kind, c.Kind)
			require.EqualValues(t, 0, c.Match)

			if c.Kind == 3 {
				require.Equal(t, tc.data, c.Data)
			} else {
				require.Equal(t, tc.data, c.Indirect)
			}

			require.Equal(t, tc.mask, c.Mask)
			require.Equal(t, tc.bytes, c.Bytes)
			require.Equal(t, tc.bits, c.Bits)
		})
	}
}

func TestCompile_InvalidRule(t *testing.T) {
	t.Parallel()

	cases := map[string]wfp.Rule{
		"action": {Layer: wfp.LayerConnectV4},
		"layer":  {Action: wfp.ActionPermit},
		"weight": {Action: wfp.ActionPermit, Layer: wfp.LayerConnectV4, Weight: 16},
		"field":  {Action: wfp.ActionPermit, Layer: wfp.LayerConnectV4, Conditions: []wfp.Condition{{}}},
		"prefix": {Action: wfp.ActionPermit,
			Layer:      wfp.LayerConnectV6,
			Conditions: []wfp.Condition{{Field: wfp.FieldRemoteAddress}}},
		"family": {Action: wfp.ActionPermit,
			Layer:      wfp.LayerConnectV6,
			Conditions: []wfp.Condition{{Field: wfp.FieldRemoteAddress, Prefix: netip.MustParsePrefix("203.0.113.1/32")}}},
		"mapped": {Action: wfp.ActionPermit,
			Layer: wfp.LayerConnectV4,
			Conditions: []wfp.Condition{{Field: wfp.FieldRemoteAddress,
				Prefix: netip.MustParsePrefix("::ffff:203.0.113.1/128")}}},
	}
	for name, rule := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rules := append(wfp.NewRules(77, nil, nil, false), rule)
			_, _, err := wfp.CompileForTest("sentinel-dvpn-c0", rules)
			require.ErrorIs(t, err, wfp.ErrInvalidRule)

			if name == "prefix" {
				require.ErrorContains(t, err, "invalid prefix")
			}
		})
	}
}

func TestCompile_Scalars(t *testing.T) {
	t.Parallel()

	conditions := []wfp.Condition{
		{Field: wfp.FieldFlags, Flags: 1},
		{Field: wfp.FieldLocalInterface, InterfaceLUID: 77},
		{Field: wfp.FieldProtocol, Protocol: 6},
		{Field: wfp.FieldRemoteAddress, Prefix: netip.MustParsePrefix("203.0.113.1/32")},
		{Field: wfp.FieldRemotePort, Port: 443},
	}
	views, _, err := wfp.CompileForTest("sentinel-dvpn-c0", []wfp.Rule{
		{Action: wfp.ActionPermit, Conditions: conditions, Layer: wfp.LayerConnectV4},
	})
	require.NoError(t, err)

	for i, c := range views[0].Conditions {
		require.Equal(t, wfp.ConditionGUIDs()[i], c.Field.String())
		require.Equal(t, []uint32{3, 4, 1, 3, 2}[i], c.Kind)

		if i != 1 {
			require.Equal(t, []uint64{1, 0, 6, 0xcb007101, 443}[i], c.Data)
		}
	}
}
