package testutil_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
)

func TestNewClientHome_StreamsSessionEvents(t *testing.T) {
	t.Parallel()

	for name, newHome := range map[string]func(*testing.T, *sdktest.FakeRPC) testutil.Home{
		"client":   testutil.NewClientHome,
		"provider": testutil.NewProviderHome,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			home := newHome(t, sdktest.NewFakeRPC(t))
			home.NodeService.SetHeartbeatInterval(time.Millisecond)
			home.NodeService.SetUsage(session.Usage{RxBytes: 1})
			sessionID := home.NodeService.SessionIDs()[0]
			httpClient := api.NewPinnedHTTPClient(api.Fingerprint(home.NodeServer.Certificate()), 0)
			httpClient.Transport = &api.SignedTransport{Base: httpClient.Transport, Signer: home.DaemonKey}
			t.Cleanup(httpClient.CloseIdleConnections)
			client, err := nodeapi.NewClient(home.NodeServer.URL, nodeapi.WithHTTPClient(httpClient))
			require.NoError(t, err)

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			response, err := client.GetSessionEvents(ctx, sessionID)

			require.NoError(t, err)
			defer func() { require.NoError(t, response.Body.Close()) }()

			require.Equal(t, http.StatusOK, response.StatusCode)
			require.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
			home.NodeService.PublishVoucherRequest()

			decoder := api.NewEventDecoder(response.Body)
			for {
				event, decodeErr := decoder.Next()
				require.NoError(t, decodeErr)

				if event.IsComment {
					continue
				}

				require.Equal(t, nodeapi.EventVoucherRequest, event.Name)

				var snapshot nodeapi.VoucherRequestEvent
				require.NoError(t, json.Unmarshal(event.Data, &snapshot))
				require.Equal(t, uint64(1), snapshot.Consumption.OwedBaseUnits)

				break
			}

			closed, err := client.CloseSession(t.Context(), sessionID)
			require.NoError(t, err)
			require.NoError(t, closed.Body.Close())
			require.Equal(t, http.StatusNoContent, closed.StatusCode)

			for {
				event, decodeErr := decoder.Next()
				require.NoError(t, decodeErr)

				if event.IsComment {
					continue
				}

				require.Equal(t, nodeapi.EventClosed, event.Name)

				break
			}

			_, err = decoder.Next()
			require.ErrorIs(t, err, io.EOF)
			reopened, err := client.GetSessionEvents(t.Context(), sessionID)
			require.NoError(t, err)
			require.NoError(t, reopened.Body.Close())
			require.Equal(t, http.StatusOK, reopened.StatusCode)

			other, err := nodeapi.NewPinnedClient(home.NodeServer.URL,
				api.Fingerprint(home.NodeServer.Certificate()), solana.NewWallet().PrivateKey, 0)
			require.NoError(t, err)
			denied, err := other.GetSessionWithResponse(t.Context(), sessionID)
			require.NoError(t, err)
			require.Equal(t, http.StatusUnauthorized, denied.StatusCode())
		})
	}
}
