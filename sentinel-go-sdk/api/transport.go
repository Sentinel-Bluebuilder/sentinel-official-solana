package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/solana-foundation/solana-go/v2"

	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
)

const (
	defaultResponseHeaderTimeout = 15 * time.Second
	maxResponseHeaderBytes       = 64 << 10
	retryAfterHeader             = "Retry-After"
	retryAttempts                = 5
	retryBaseDelay               = 500 * time.Millisecond
	retryBudget                  = 10 * time.Second
	retryDrainLimitBytes         = 4 << 10
)

var (
	ErrFingerprintMismatch = errors.New("server certificate fingerprint mismatch")
	ErrUnexpectedRedirect  = errors.New("unexpected redirect")
)

type RetryTransport struct {
	Base http.RoundTripper

	wait func(context.Context, time.Duration) error
}

func (t *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	wait := t.wait
	if wait == nil {
		wait = sleep
	}

	next := req
	budget := retryBudget

	for attempt := 1; ; attempt++ {
		resp, err := base.RoundTrip(next)
		if err != nil && attempt > 1 {
			return nil, fmt.Errorf("attempt %d: %w", attempt, err)
		}

		if err != nil {
			return nil, err //nolint:wrapcheck
		}

		delay, ok := retryDelay(req, resp, attempt, budget)
		if !ok {
			return resp, nil
		}

		_, _ = io.CopyN(io.Discard, resp.Body, retryDrainLimitBytes)
		_ = resp.Body.Close()

		if err := wait(req.Context(), delay); err != nil {
			return nil, err
		}

		budget -= delay

		next, err = rewind(req)
		if err != nil {
			return nil, err
		}
	}
}

type SignedTransport struct {
	Base   http.RoundTripper
	Signer solana.PrivateKey
}

func (t *SignedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())

	var body []byte

	if req.Body != nil {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("reading request body: %w", err)
		}

		if err := req.Body.Close(); err != nil {
			return nil, fmt.Errorf("closing request body: %w", err)
		}

		body = raw
		req.Body = io.NopCloser(bytes.NewReader(raw))
		req.ContentLength = int64(len(raw))
	}

	sig, err := auth.SignRequest(t.Signer, req.Method, req.URL.Path, body, time.Now())
	if err != nil {
		return nil, fmt.Errorf("signing request: %w", err)
	}

	sig.Apply(req.Header)

	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	resp, err := base.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("performing request: %w", err)
	}

	return resp, nil
}

type TransportOption func(*http.Transport)

func NewInsecureHTTPClient(timeout time.Duration, opts ...TransportOption) *http.Client {
	return newHTTPClient(timeout, nil, opts)
}

func NewPinnedHTTPClient(fingerprint string, timeout time.Duration, opts ...TransportOption) *http.Client {
	verify := func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 || Fingerprint(cs.PeerCertificates[0]) != fingerprint {
			return ErrFingerprintMismatch
		}

		return nil
	}

	return newHTTPClient(timeout, verify, opts)
}

func WithDialContext(dial func(ctx context.Context, network, address string) (net.Conn, error)) TransportOption {
	return func(t *http.Transport) {
		t.DialContext = dial
	}
}

func newHTTPClient(timeout time.Duration, verify func(tls.ConnectionState) error, opts []TransportOption) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec
		MinVersion:         tls.VersionTLS12,
		VerifyConnection:   verify,
	}

	transport.Proxy = nil
	transport.MaxResponseHeaderBytes = maxResponseHeaderBytes
	transport.ResponseHeaderTimeout = defaultResponseHeaderTimeout

	if timeout > 0 {
		transport.ResponseHeaderTimeout = timeout
	}

	for _, opt := range opts {
		opt(transport)
	}

	return &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return ErrUnexpectedRedirect },
		Timeout:       timeout,
		Transport:     transport,
	}
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	secs, err := strconv.ParseUint(value, 10, 64)
	if err == nil || errors.Is(err, strconv.ErrRange) {
		return time.Duration(min(secs, uint64(math.MaxInt64/time.Second))) * time.Second, true
	}

	retryAt, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}

	return max(retryAt.Sub(now), 0), true
}

func retryDelay(req *http.Request, resp *http.Response, attempt int, budget time.Duration) (time.Duration, bool) {
	if resp.StatusCode != http.StatusTooManyRequests || attempt >= retryAttempts {
		return 0, false
	}

	if req.GetBody == nil && req.Body != nil && req.Body != http.NoBody {
		return 0, false
	}

	delay, ok := parseRetryAfter(resp.Header.Get(retryAfterHeader), time.Now())
	if !ok {
		delay = retryBaseDelay << (attempt - 1)
	}

	if delay > budget {
		return 0, false
	}

	if deadline, ok := req.Context().Deadline(); ok && time.Until(deadline) <= delay {
		return 0, false
	}

	return delay, true
}

func rewind(req *http.Request) (*http.Request, error) {
	next := req.Clone(req.Context())
	if req.GetBody == nil {
		return next, nil
	}

	body, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("rewinding request body: %w", err)
	}

	next.Body = body

	return next, nil
}

func sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("waiting %s to retry: %w", delay, ctx.Err())
	case <-timer.C:
		return nil
	}
}
