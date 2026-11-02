package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"time"
)

var ErrMissingPeerAddrs = errMissingPeerAddrs

func NewPeerDialerForTest(
	lookup func(ctx context.Context, network, host string) ([]netip.Addr, error),
	allowPrivate bool,
	dial func(ctx context.Context, network, addr string) (net.Conn, error),
) func(ctx context.Context, network, addr string) (net.Conn, error) {
	p := &peerDialer{
		allowPrivate: allowPrivate,
		dial:         dial,
		lookup:       lookup,
	}

	return p.dialContext
}

func NewRetryTransportForTest(
	base http.RoundTripper,
	wait func(context.Context, time.Duration) error,
) *RetryTransport {
	return &RetryTransport{Base: base, wait: wait}
}
