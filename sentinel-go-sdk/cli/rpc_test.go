package cli_test

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/solana-foundation/solana-go/v2/rpc/jsonrpc"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
)

func TestDefaultRPCConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		network string
		url     string
		wsURL   string
	}{
		{network: cli.NetworkDevnet, url: "https://api.devnet.solana.com", wsURL: "wss://api.devnet.solana.com"},
		{network: cli.NetworkLocalnet, url: "http://127.0.0.1:8899", wsURL: "ws://127.0.0.1:8900"},
		{network: cli.NetworkMainnet, url: "https://api.mainnet-beta.solana.com", wsURL: "wss://api.mainnet-beta.solana.com"},
	}

	for _, tc := range cases {
		t.Run(tc.network, func(t *testing.T) {
			t.Parallel()

			got := cli.DefaultRPCConfig(tc.network)
			require.Equal(t, rpc.CommitmentConfirmed, got.Commitment)
			require.Equal(t, 10*time.Second, got.Timeout)
			require.Equal(t, tc.url, got.URL)
			require.Equal(t, tc.wsURL, got.WSURL)
			require.NoError(t, got.Validate())
		})
	}
}

func TestRedactURL_KeepsOnlySchemeAndHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw  string
		want string
	}{
		{raw: "%zz", want: ""},
		{raw: "http://127.0.0.1:8899/", want: "http://127.0.0.1:8899"},
		{raw: "https://mainnet.helius-rpc.com/?api-key=SECRETQ", want: "https://mainnet.helius-rpc.com"},
		{raw: decorate(t, "https://x.quiknode.pro:8443") + "#SECRETFRAG", want: "https://x.quiknode.pro:8443"},
		{raw: "wss://api.mainnet-beta.solana.com", want: "wss://api.mainnet-beta.solana.com"},
	}

	for _, tc := range cases {
		require.Equal(t, tc.want, cli.RedactURL(tc.raw), tc.raw)
	}
}

func TestRPCConfig_NewClient_ErrorsNameOnlyTheEndpointHost(t *testing.T) {
	t.Parallel()

	refused := refusedURL(t)
	failing := serve(t, http.StatusInternalServerError, "boom")
	garbage := serve(t, http.StatusOK, "not json")

	cases := []struct {
		base  string
		check func(t *testing.T, err error)
		name  string
	}{
		{
			base: refused,
			check: func(t *testing.T, err error) {
				t.Helper()

				uerr, ok := errors.AsType[*url.Error](err)
				require.True(t, ok)
				require.Equal(t, refused, uerr.URL)

				_, ok = errors.AsType[net.Error](err)
				require.True(t, ok)
			},
			name: "refused",
		},
		{
			base: failing,
			check: func(t *testing.T, err error) {
				t.Helper()

				httpErr, ok := errors.AsType[*jsonrpc.HTTPError](err)
				require.True(t, ok)
				require.Equal(t, http.StatusInternalServerError, httpErr.Code)
			},
			name: "status 500",
		},
		{
			base:  garbage,
			check: func(*testing.T, error) {},
			name:  "garbage body",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := cli.DefaultRPCConfig(cli.NetworkLocalnet)
			cfg.URL = decorate(t, tc.base)
			client := cfg.NewClient()

			_, err := client.GetSlot(t.Context(), rpc.CommitmentConfirmed)
			requireOnlyHost(t, err, tc.base)
			tc.check(t, err)

			_, err = client.RPCCallBatch(t.Context(), jsonrpc.RPCRequests{jsonrpc.NewRequest("getSlot")})
			requireOnlyHost(t, err, tc.base)

			ignore := func(*http.Request, *http.Response) error { return nil }
			err = client.RPCCallWithCallback(t.Context(), "getSlot", nil, ignore)

			if tc.base == refused {
				requireOnlyHost(t, err, tc.base)
			}
		})
	}
}
