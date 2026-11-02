package client_test

import (
	"context"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/client"
)

func TestNewFirewall_Windows(t *testing.T) {
	t.Parallel()

	require.IsType(t, client.WFPFirewall{}, client.NewFirewall(t.TempDir(), "sentinel-dvpn-c0"))
}

func TestWFPFirewall_CanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	firewall := client.NewFirewall(t.TempDir(), "sentinel-dvpn-c0")

	require.ErrorIs(t, firewall.Apply(ctx, "missing-iface", []netip.Addr{
		netip.MustParseAddr("203.0.113.1"),
	}, nil, false), context.Canceled)
	require.ErrorIs(t, firewall.Flush(ctx), context.Canceled)

	loaded, err := client.KillSwitchLoaded(ctx, "sentinel-dvpn-c0")
	require.False(t, loaded)
	require.ErrorIs(t, err, context.Canceled)
}
