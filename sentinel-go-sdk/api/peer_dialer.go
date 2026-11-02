package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
)

var errMissingPeerAddrs = errors.New("missing peer addresses")

type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

type lookupFunc func(ctx context.Context, network, host string) ([]netip.Addr, error)

type peerDialer struct {
	allowPrivate bool
	dial         dialFunc
	lookup       lookupFunc
}

func NewPeerDialer(
	lookup func(ctx context.Context, network, host string) ([]netip.Addr, error),
	allowPrivate bool,
) func(ctx context.Context, network, addr string) (net.Conn, error) {
	p := &peerDialer{
		allowPrivate: allowPrivate,
		dial:         (&net.Dialer{}).DialContext,
		lookup:       lookup,
	}

	return p.dialContext
}

func (p *peerDialer) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("splitting peer address: %w", err)
	}

	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("parsing peer port: %w", err)
	}

	addrs, err := p.resolve(ctx, host)
	if err != nil {
		return nil, err
	}

	var errs []error

	for _, a := range addrs {
		conn, err := p.dial(ctx, network, netip.AddrPortFrom(a, uint16(port)).String())
		if err == nil {
			return conn, nil
		}

		errs = append(errs, err)
	}

	return nil, errors.Join(errs...)
}

func (p *peerDialer) lookupHost(ctx context.Context, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a}, nil
	}

	return p.lookup(ctx, "ip", host)
}

func (p *peerDialer) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	found, err := p.lookupHost(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolving %q: %w", host, err)
	}

	addrs := make([]netip.Addr, 0, len(found))
	for _, a := range found {
		addrs = append(addrs, a.Unmap().WithZone(""))
	}

	slices.SortFunc(addrs, netip.Addr.Compare)
	addrs = slices.Compact(addrs)

	if len(addrs) == 0 {
		return nil, fmt.Errorf("resolving %q: %w", host, errMissingPeerAddrs)
	}

	if p.allowPrivate {
		return addrs, nil
	}

	for _, a := range addrs {
		if err := ValidatePublicAddr(a); err != nil {
			return nil, err
		}
	}

	return addrs, nil
}
