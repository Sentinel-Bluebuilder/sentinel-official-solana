package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/solana-foundation/solana-go/v2/rpc/jsonrpc"
	"github.com/solana-foundation/solana-go/v2/rpc/ws"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
)

const (
	NetworkDevnet   = "devnet"
	NetworkLocalnet = "localnet"
	NetworkMainnet  = "mainnet"
)

const (
	rpcKeepAlive           = 180 * time.Second
	rpcMaxConnsPerHost     = 9
	rpcTLSHandshakeTimeout = 10 * time.Second
)

//nolint:gochecknoglobals
var defaultURLs = map[string][2]string{
	NetworkDevnet:   {"https://api.devnet.solana.com", "wss://api.devnet.solana.com"},
	NetworkLocalnet: {"http://127.0.0.1:8899", "ws://127.0.0.1:8900"},
	NetworkMainnet:  {"https://api.mainnet-beta.solana.com", "wss://api.mainnet-beta.solana.com"},
}

type RPCConfig struct {
	Commitment rpc.CommitmentType `json:"commitment" toml:"commitment" validate:"oneof=processed confirmed finalized"`
	Timeout    time.Duration      `json:"timeout"    toml:"timeout"    validate:"gt=0"`
	URL        string             `json:"url"        toml:"url"        validate:"required,url"`
	WSURL      string             `json:"ws_url"     toml:"ws_url"     validate:"required,url"`
}

func (c RPCConfig) Connect(ctx context.Context) (*rpc.Client, *ws.Client, error) {
	if err := c.Validate(); err != nil {
		return nil, nil, err
	}

	wsClient, err := c.DialWS(ctx)
	if err != nil {
		return nil, nil, err
	}

	return c.NewClient(), wsClient, nil
}

func (c RPCConfig) Dialer() net.Dialer {
	return net.Dialer{
		KeepAlive: rpcKeepAlive,
		Timeout:   c.Timeout,
	}
}

func (c RPCConfig) DialWS(ctx context.Context) (*ws.Client, error) {
	client, err := ws.ConnectWithOptions(ctx, c.WSURL, &ws.Options{HandshakeTimeout: c.Timeout})
	if err != nil {
		return nil, fmt.Errorf("connecting websocket %s: %w", RedactURL(c.WSURL), err)
	}

	return client, nil
}

func (c RPCConfig) NewClient(opts ...api.TransportOption) *rpc.Client {
	dialer := c.Dialer()
	transport := &http.Transport{
		DialContext:         dialer.DialContext,
		ForceAttemptHTTP2:   true,
		IdleConnTimeout:     c.Timeout,
		MaxConnsPerHost:     rpcMaxConnsPerHost,
		MaxIdleConnsPerHost: rpcMaxConnsPerHost,
		Proxy:               http.ProxyFromEnvironment,
		TLSHandshakeTimeout: rpcTLSHandshakeTimeout,
	}

	for _, opt := range opts {
		opt(transport)
	}

	httpClient := &http.Client{
		Timeout:   c.Timeout,
		Transport: transport,
	}
	jsonOpts := &jsonrpc.RPCClientOpts{HTTPClient: httpClient}

	return rpc.NewWithCustomRPCClient(newRedactingRPC(c.URL, jsonrpc.NewClientWithOpts(c.URL, jsonOpts)))
}

func (c RPCConfig) Validate() error {
	return validate.Struct(c)
}

type redactedError struct {
	err error
	msg string
}

func (e redactedError) Error() string {
	return e.msg
}

func (e redactedError) Unwrap() error {
	return e.err
}

type redactingRPC struct {
	inner    rpc.JSONRPCClient
	raw      []string
	redacted string
}

func newRedactingRPC(endpoint string, inner rpc.JSONRPCClient) redactingRPC {
	redacting := redactingRPC{
		inner:    inner,
		redacted: RedactURL(endpoint),
	}

	for _, form := range endpointForms(endpoint) {
		if form != "" && form != redacting.redacted {
			redacting.raw = append(redacting.raw, form)
		}
	}

	return redacting
}

func (r redactingRPC) CallBatch(ctx context.Context, requests jsonrpc.RPCRequests) (jsonrpc.RPCResponses, error) {
	resp, err := r.inner.CallBatch(ctx, requests)

	return resp, r.redact(err)
}

func (r redactingRPC) CallForInto(ctx context.Context, out any, method string, params []any) error {
	return r.redact(r.inner.CallForInto(ctx, out, method, params))
}

func (r redactingRPC) CallWithCallback(
	ctx context.Context,
	method string,
	params []any,
	callback func(*http.Request, *http.Response) error,
) error {
	return r.redact(r.inner.CallWithCallback(ctx, method, params, callback))
}

func (r redactingRPC) Close() error {
	closer, ok := r.inner.(io.Closer)
	if !ok {
		return nil
	}

	if err := closer.Close(); err != nil {
		return fmt.Errorf("closing rpc client: %w", err)
	}

	return nil
}

func (r redactingRPC) redact(err error) error {
	if err == nil {
		return nil
	}

	if uerr, ok := errors.AsType[*url.Error](err); ok {
		uerr.URL = r.redacted
	}

	orig := err.Error()

	msg := orig
	for _, form := range r.raw {
		msg = strings.ReplaceAll(msg, form, r.redacted)
	}

	if msg == orig {
		return err
	}

	return redactedError{
		err: err,
		msg: msg,
	}
}

func DefaultRPCConfig(network string) RPCConfig {
	endpoints := defaultURLs[network]

	return RPCConfig{
		Commitment: rpc.CommitmentConfirmed,
		Timeout:    10 * time.Second,
		URL:        endpoints[0],
		WSURL:      endpoints[1],
	}
}

func RedactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	redacted := &url.URL{
		Host:   parsed.Host,
		Scheme: parsed.Scheme,
	}

	return redacted.String()
}

func endpointForms(raw string) []string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return []string{raw}
	}

	forms := []string{raw, parsed.String()}
	if _, ok := parsed.User.Password(); ok {
		forms = append(forms, strings.Replace(parsed.String(), parsed.User.String()+"@", parsed.User.Username()+":***@", 1))
	}

	return forms
}
