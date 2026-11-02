package client

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/sentinel-official/sentinel-dvpnd/internal/wfp"
)

type wfpFirewall struct {
	name string
}

func (f wfpFirewall) Apply(
	ctx context.Context,
	iface string,
	nodeAddrs []netip.Addr,
	rpcAndWSAddrs []netip.AddrPort,
	allowLAN bool,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("applying firewall: %w", err)
	}

	var luid uint64

	if iface != "" {
		var err error

		luid, err = wfp.InterfaceLUID(iface)
		if err != nil {
			return fmt.Errorf("applying firewall: %w", err)
		}
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("applying firewall: %w", err)
	}

	if err := wfp.Apply(f.name, wfp.NewRules(luid, nodeAddrs, rpcAndWSAddrs, allowLAN)); err != nil {
		return fmt.Errorf("applying firewall: %w: %w", errFirewallChanged, err)
	}

	return nil
}

func (f wfpFirewall) Flush(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("flushing firewall: %w", err)
	}

	if err := wfp.Flush(f.name); err != nil {
		return fmt.Errorf("flushing firewall: %w", err)
	}

	return nil
}

func KillSwitchLoaded(ctx context.Context, name string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("inspecting firewall: %w", err)
	}

	loaded, err := wfp.Loaded(name)
	if err != nil {
		return false, fmt.Errorf("inspecting firewall: %w", err)
	}

	return loaded, nil
}

func newFirewall(_, name string) firewallBackend { return wfpFirewall{name: name} }
