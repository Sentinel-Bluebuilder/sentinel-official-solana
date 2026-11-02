package client

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
)

var errUnknownScheme = errors.New("rpc url has no port and an unknown scheme")

type allowlist struct {
	rpcAddrs      []netip.AddrPort
	rpcAndWSAddrs []netip.AddrPort
}

type nodeHosts struct {
	addrs []netip.Addr
	uri   api.PeerURL
}

func (r *Runtime) resolveAllowlist(ctx context.Context) (allowlist, error) {
	if r.cfg.Network.KillSwitch == config.KillSwitchOff {
		return allowlist{}, nil
	}

	var row Firewall

	rpcCfg := r.rpcCfg

	err := r.findFirst(ctx, &row)

	switch {
	case err == nil && row.RPCURL == rpcCfg.URL && row.WSURL == rpcCfg.WSURL:
		if row.RPCAndWSAddrs == "" {
			return allowlist{}, errFirewallRowIncomplete
		}

		_, rpcAndWSAddrs, rpcAddrs, err := parseFirewall(row)

		return allowlist{
			rpcAndWSAddrs: rpcAndWSAddrs,
			rpcAddrs:      rpcAddrs,
		}, err
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
		return allowlist{}, err
	}

	rpcAddrs, err := r.resolveRPCAddrPorts(ctx, rpcCfg.URL)
	if err != nil {
		return allowlist{}, err
	}

	wsAddrs, err := r.resolveRPCAddrPorts(ctx, rpcCfg.WSURL)
	if err != nil {
		return allowlist{}, err
	}

	return allowlist{
		rpcAddrs:      rpcAddrs,
		rpcAndWSAddrs: mergeAddrPorts(rpcAddrs, wsAddrs),
	}, nil
}

func (r *Runtime) resolveRPCAddrPorts(ctx context.Context, rawURL string) ([]netip.AddrPort, error) {
	lctx, cancel := r.withTimeout(ctx)
	defer cancel()

	return resolveEndpoint(lctx, r.lookup, rawURL)
}

func formatAddrPorts(addrs []netip.AddrPort) string {
	fields := make([]string, 0, len(addrs))
	for _, ap := range addrs {
		fields = append(fields, ap.String())
	}

	return strings.Join(fields, " ")
}

func formatAddrs(addrs []netip.Addr) string {
	fields := make([]string, 0, len(addrs))
	for _, a := range addrs {
		fields = append(fields, a.String())
	}

	return strings.Join(fields, " ")
}

func mergeAddrPorts(sets ...[]netip.AddrPort) []netip.AddrPort {
	addrs := slices.Concat(sets...)
	slices.SortFunc(addrs, netip.AddrPort.Compare)

	return slices.Compact(addrs)
}

func mergeAddrs(sets ...[]netip.Addr) []netip.Addr {
	addrs := slices.Concat(sets...)
	slices.SortFunc(addrs, netip.Addr.Compare)

	return slices.Compact(addrs)
}

func parseAddrPorts(s string) ([]netip.AddrPort, error) {
	fields := strings.Fields(s)
	addrs := make([]netip.AddrPort, 0, len(fields))

	for _, field := range fields {
		ap, err := netip.ParseAddrPort(field)
		if err != nil {
			return nil, fmt.Errorf("parsing kill switch rpc endpoint: %w", err)
		}

		addrs = append(addrs, ap)
	}

	return addrs, nil
}

func parseAddrs(s string) ([]netip.Addr, error) {
	fields := strings.Fields(s)
	addrs := make([]netip.Addr, 0, len(fields))

	for _, field := range fields {
		a, err := netip.ParseAddr(field)
		if err != nil {
			return nil, fmt.Errorf("parsing kill switch node address: %w", err)
		}

		addrs = append(addrs, a)
	}

	return addrs, nil
}

func parseFirewall(row Firewall) ([]netip.Addr, []netip.AddrPort, []netip.AddrPort, error) {
	node, err := parseAddrs(row.NodeAddrs)
	if err != nil {
		return nil, nil, nil, err
	}

	rpcAndWSAddrs, err := parseAddrPorts(row.RPCAndWSAddrs)
	if err != nil {
		return nil, nil, nil, err
	}

	rpcAddrs, err := parseAddrPorts(row.RPCAddrs)
	if err != nil {
		return nil, nil, nil, err
	}

	return node, rpcAndWSAddrs, rpcAddrs, nil
}

func resolveEndpoint(
	ctx context.Context,
	lookup func(context.Context, string, string) ([]netip.Addr, error),
	rawURL string,
) ([]netip.AddrPort, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parsing rpc url: %w", err)
	}

	port, err := urlPort(u)
	if err != nil {
		return nil, err
	}

	ips, err := lookup(ctx, "ip", u.Hostname())
	if err != nil {
		return nil, fmt.Errorf("resolving rpc host %s: %w", u.Hostname(), err)
	}

	addrs := make([]netip.AddrPort, 0, len(ips))
	for _, ip := range ips {
		addrs = append(addrs, netip.AddrPortFrom(ip.Unmap().WithZone(""), port))
	}

	return mergeAddrPorts(addrs), nil
}

func urlPort(u *url.URL) (uint16, error) {
	if p := u.Port(); p != "" {
		n, err := strconv.ParseUint(p, 10, 16)
		if err != nil {
			return 0, fmt.Errorf("rpc url port: %w", err)
		}

		return uint16(n), nil
	}

	switch u.Scheme {
	case "http", "ws":
		return 80, nil
	case "https", "wss":
		return 443, nil
	default:
		return 0, fmt.Errorf("%w: %s", errUnknownScheme, cli.RedactURL(u.String()))
	}
}

func withPort(addrs []netip.Addr, port uint16) []netip.AddrPort {
	output := make([]netip.AddrPort, 0, len(addrs))
	for _, a := range addrs {
		output = append(output, netip.AddrPortFrom(a, port))
	}

	return output
}
