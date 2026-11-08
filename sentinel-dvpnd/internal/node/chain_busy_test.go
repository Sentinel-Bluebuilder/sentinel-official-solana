//go:build linux

package node_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/node"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
)

// A node whose RPC answers 429 used to fail the request as a 500 "internal error", which reached
// the client as an opaque StartSession failure right after it had opened its channel.

func TestHandler_GetLease_BriefRateLimitIsRetried(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	address := seedHandlerLease(t, r, home)

	home.RPC.RateLimited = map[string]int{"getAccountInfo": 1, "getMultipleAccounts": 1}

	response, err := node.NewHandler(r).GetLease(newSignedContext(t, home.OwnerAddress), nodeapi.GetLeaseRequestObject{Lease: address})
	require.NoError(t, err, "one 429 on the node's chain read is retried")

	_, ok := response.(nodeapi.GetLease200JSONResponse)
	require.True(t, ok)
}

func TestHandler_GetLease_LastingRateLimitAnswersBusy(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	address := seedHandlerLease(t, r, home)

	home.RPC.RateLimited = map[string]int{"getAccountInfo": 1_000, "getMultipleAccounts": 1_000}

	_, err := node.NewHandler(r).GetLease(newSignedContext(t, home.OwnerAddress), nodeapi.GetLeaseRequestObject{Lease: address})
	require.Error(t, err)

	apiErr, ok := errors.AsType[*api.Error](err)
	require.True(t, ok, "the error carries an API answer, not an internal error: %v", err)
	require.Equal(t, api.CodeRateLimited, apiErr.Code)
	require.Equal(t, http.StatusTooManyRequests, apiErr.HTTPStatus)
	require.Equal(t, "the node's network service is busy, try again", apiErr.Message)
}

func TestHandler_GetLease_AccountGoneIsNotRetriedAsBusy(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	seedHandlerLease(t, r, home)

	_, err := node.NewHandler(r).GetLease(newSignedContext(t, home.OwnerAddress), nodeapi.GetLeaseRequestObject{Lease: [32]byte{56}})
	require.ErrorIs(t, err, api.ErrChannelNotOpen, "a definite answer stays as it is")
}
