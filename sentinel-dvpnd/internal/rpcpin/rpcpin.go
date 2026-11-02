package rpcpin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2/rpc/ws"

	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
)

const minAttemptTimeout = 2 * time.Second

var (
	ErrRPCUnreachable = errors.New("no admitted address answered")
	ErrUnknownScheme  = errors.New("rpc url has no port and an unknown scheme")
)

type Pin struct {
	addr   string
	dialer net.Dialer
	hosts  map[string]*pinnedHost
	mu     sync.RWMutex
}

func (p *Pin) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()

	clear(p.hosts)
}

func (p *Pin) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host := p.host(addr)

	if host == nil {
		conn, err := p.dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, fmt.Errorf("dialing: %w", err)
		}

		return conn, nil
	}

	order := host.dialOrder()
	deadline, bounded := p.deadline(ctx)
	dialErr := fmt.Errorf("%w: %s", ErrRPCUnreachable, addr)

	for i, ap := range order {
		attemptCtx, cancel := ctx, context.CancelFunc(func() {})
		if bounded {
			attemptCtx, cancel = context.WithDeadline(ctx, attemptDeadline(time.Now(), deadline, len(order)-i))
		}

		conn, err := p.dialer.DialContext(attemptCtx, network, ap.String())

		cancel()

		if err == nil {
			host.preferred.Store(&ap)

			return conn, nil
		}

		dialErr = fmt.Errorf("%w; %w", dialErr, err)
	}

	return nil, dialErr
}

func (p *Pin) Pinned() ([]netip.AddrPort, bool) {
	return p.PinnedHost(p.addr)
}

func (p *Pin) PinnedHost(addr string) ([]netip.AddrPort, bool) {
	host := p.host(addr)
	if host == nil {
		return nil, false
	}

	return slices.Clone(host.addrs), true
}

func (p *Pin) Set(addrs []netip.AddrPort) {
	p.SetHost(p.addr, addrs)
}

func (p *Pin) SetHost(addr string, addrs []netip.AddrPort) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(addrs) == 0 {
		delete(p.hosts, addr)

		return
	}

	p.hosts[addr] = &pinnedHost{addrs: slices.Clone(addrs)}
}

func (p *Pin) deadline(ctx context.Context) (time.Time, bool) {
	deadline, bounded := ctx.Deadline()

	if p.dialer.Timeout > 0 {
		byTimeout := time.Now().Add(p.dialer.Timeout)
		if !bounded || byTimeout.Before(deadline) {
			return byTimeout, true
		}
	}

	return deadline, bounded
}

func (p *Pin) host(addr string) *pinnedHost {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.hosts[addr]
}

type WSDialer func(ctx context.Context) (*ws.Client, error)

func NewWSDialer(rawURL string, timeout time.Duration, pin *Pin, log zerolog.Logger) WSDialer {
	wsURL := cli.RedactURL(rawURL)
	named := hostIsName(rawURL)

	return func(ctx context.Context) (*ws.Client, error) {
		if pin != nil && named {
			if _, active := pin.Pinned(); active {
				log.Info().Str("ws_url", wsURL).Msg("websocket skipped while the rpc is pinned, confirming by polling")

				return nil, nil
			}
		}

		cfg := cli.RPCConfig{
			Timeout: timeout,
			WSURL:   rawURL,
		}

		return cfg.DialWS(ctx)
	}
}

type pinnedHost struct {
	addrs     []netip.AddrPort
	preferred atomic.Pointer[netip.AddrPort]
}

func (h *pinnedHost) dialOrder() []netip.AddrPort {
	preferred := h.preferred.Load()
	if preferred == nil {
		return h.addrs
	}

	i := slices.Index(h.addrs, *preferred)
	if i <= 0 {
		return h.addrs
	}

	return slices.Concat(h.addrs[i:i+1], h.addrs[:i], h.addrs[i+1:])
}

func New(rawURL string, dialer net.Dialer) (*Pin, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parsing rpc url: %w", err)
	}

	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			return nil, fmt.Errorf("%w: %s", ErrUnknownScheme, cli.RedactURL(rawURL))
		}
	}

	pin := &Pin{
		dialer: dialer,
		addr:   net.JoinHostPort(u.Hostname(), port),
		hosts:  map[string]*pinnedHost{},
	}

	return pin, nil
}

func attemptDeadline(now, deadline time.Time, remaining int) time.Time {
	left := deadline.Sub(now)

	share := left / time.Duration(remaining)
	if share < minAttemptTimeout {
		share = min(left, minAttemptTimeout)
	}

	return now.Add(share)
}

func hostIsName(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	_, err = netip.ParseAddr(u.Hostname())

	return err != nil
}
