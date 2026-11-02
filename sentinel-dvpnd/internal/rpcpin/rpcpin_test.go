package rpcpin_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/rpcpin"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
)

const pinnedRPCHost = "rpc.sentinel-dvpnd.invalid"

var errDNSBlocked = errors.New("dns blocked by the kill switch")

func TestPin_DialContext_ReachesAdmittedAddressWhenDNSBlocked(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	cfg := pinnedRPCConfig(t, pinnedRPCHost, fakeRPC)
	pin, err := rpcpin.New(cfg.URL, blockedDNSDialer())
	require.NoError(t, err)

	client := cfg.NewClient(api.WithDialContext(pin.DialContext))
	_, err = client.GetSlot(t.Context(), rpc.CommitmentConfirmed)
	require.ErrorContains(t, err, pinnedRPCHost)
	require.ErrorContains(t, err, errDNSBlocked.Error())
	pin.Set([]netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:1"), fakeAddr(t, fakeRPC)})
	_, err = client.GetSlot(t.Context(), rpc.CommitmentConfirmed)
	require.NoError(t, err)
	pin.Clear()
	require.NoError(t, client.Close())
	_, err = client.GetSlot(t.Context(), rpc.CommitmentConfirmed)
	require.ErrorContains(t, err, pinnedRPCHost)
	require.ErrorContains(t, err, errDNSBlocked.Error())
}

func blockedDNSDialer() net.Dialer {
	dialer := (cli.RPCConfig{Timeout: time.Second}).Dialer()
	dialer.Resolver = &net.Resolver{
		Dial:     func(context.Context, string, string) (net.Conn, error) { return nil, errDNSBlocked },
		PreferGo: true,
	}

	return dialer
}

func fakeAddr(t *testing.T, fakeRPC *sdktest.FakeRPC) netip.AddrPort {
	t.Helper()

	u, err := url.Parse(fakeRPC.Server.URL)
	require.NoError(t, err)

	return netip.MustParseAddrPort(u.Host)
}

func pinnedRPCConfig(t *testing.T, host string, fakeRPC *sdktest.FakeRPC) cli.RPCConfig {
	t.Helper()

	cfg := cli.DefaultRPCConfig(cli.NetworkLocalnet)
	cfg.Timeout = 5 * time.Second
	cfg.URL = "http://" + net.JoinHostPort(host, strconv.Itoa(int(fakeAddr(t, fakeRPC).Port())))

	return cfg
}
