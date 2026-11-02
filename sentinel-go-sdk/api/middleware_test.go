package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
)

//nolint:paralleltest
func TestLimitByIP(t *testing.T) {
	gin.SetMode(gin.TestMode)

	engine := newLimitedEngine(api.LimitByIP(api.NewLimiter(0.25, 1)))

	first := serveLimited(t, engine, nil)
	require.Equal(t, http.StatusOK, first.Code)
	require.Empty(t, first.Header().Get("Retry-After"))

	limited := serveLimited(t, engine, nil)
	require.Equal(t, http.StatusTooManyRequests, limited.Code)
	require.Equal(t, "4", limited.Header().Get("Retry-After"))
}

func TestLimiter_Allow(t *testing.T) {
	t.Parallel()

	limiter := api.NewLimiter(1, 2)

	require.True(t, allows(limiter, "a"))
	require.True(t, allows(limiter, "a"))
	require.False(t, allows(limiter, "a"))
	require.True(t, allows(limiter, "b"))
}

//nolint:paralleltest
func TestRequireSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)

	key, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	options := api.EngineOptions{
		Logger:       zerolog.Nop(),
		MaxBodyBytes: 1 << 20,
	}
	engine := api.NewEngine(options)

	handler := func(c *gin.Context) {
		signer, ok := api.SignerFrom(c)
		require.True(t, ok)
		c.String(http.StatusOK, signer.String())
	}
	engine.POST("/signed", api.RequireSignature(auth.NewNonceCache(time.Minute)), handler)

	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)

	ctx := t.Context()

	transport := &api.SignedTransport{
		Base:   http.DefaultTransport,
		Signer: key,
	}
	client := &http.Client{Transport: transport}

	signedReq, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/signed", strings.NewReader(`{"x":1}`))
	require.NoError(t, err)
	signedReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(signedReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	plainReq, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/signed", strings.NewReader(`{"x":1}`))
	require.NoError(t, err)
	plainReq.Header.Set("Content-Type", "application/json")

	plainResp, err := http.DefaultClient.Do(plainReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, plainResp.StatusCode)
	require.NoError(t, plainResp.Body.Close())
}
