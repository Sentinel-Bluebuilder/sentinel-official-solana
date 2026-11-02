package client_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/client"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
)

func TestRuntime_ReadStream_StopsOnInvalidResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	streamClient, err := nodeapi.NewInsecureClient(srv.URL, 0)
	require.NoError(t, err)

	received, err := client.ReadStream(t.Context(), nil, streamClient, "session")

	require.False(t, received)
	require.ErrorIs(t, err, api.ErrInvalidResponse)
}

func TestRuntime_ReadStream_StopsOnTerminalResponse(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		err    error
		name   string
		status int
	}{
		{err: api.ErrUnauthorized, name: "unauthorized", status: http.StatusUnauthorized},
		{err: api.ErrSessionNotFound, name: "missing session", status: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			t.Cleanup(srv.Close)
			streamClient, err := nodeapi.NewClientWithResponses(srv.URL)
			require.NoError(t, err)

			received, err := client.ReadStream(t.Context(), nil, streamClient, "session")

			require.False(t, received)
			require.ErrorIs(t, err, tc.err)
		})
	}
}
