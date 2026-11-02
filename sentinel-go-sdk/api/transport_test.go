package api_test

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
)

func TestNewInsecureHTTPClient_TransportBounds(t *testing.T) {
	t.Parallel()

	transport, ok := api.NewInsecureHTTPClient(0).Transport.(*http.Transport)
	require.True(t, ok)
	require.Nil(t, transport.Proxy)
	require.EqualValues(t, 64<<10, transport.MaxResponseHeaderBytes)
	require.Equal(t, 15*time.Second, transport.ResponseHeaderTimeout)

	transport, ok = api.NewInsecureHTTPClient(3 * time.Second).Transport.(*http.Transport)
	require.True(t, ok)
	require.Equal(t, 3*time.Second, transport.ResponseHeaderTimeout)
}

func TestNewPinnedHTTPClient(t *testing.T) {
	t.Parallel()

	cert, fingerprint, err := api.LoadOrCreateServerCert(t.TempDir())
	require.NoError(t, err)

	handler := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	srv := httptest.NewUnstartedServer(http.HandlerFunc(handler))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	srv.StartTLS()

	t.Cleanup(srv.Close)

	ctx := t.Context()

	resp, err := get(ctx, api.NewPinnedHTTPClient(fingerprint, time.Second), srv.URL)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	bad, err := get(ctx, api.NewPinnedHTTPClient("00"+fingerprint[2:], time.Second), srv.URL)
	require.Error(t, err)

	if bad != nil {
		require.NoError(t, bad.Body.Close())
	}

	resp, err = get(ctx, api.NewInsecureHTTPClient(time.Second), srv.URL)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}

func TestNewPinnedHTTPClient_RefusesRedirect(t *testing.T) {
	t.Parallel()

	cert, fingerprint, err := api.LoadOrCreateServerCert(t.TempDir())
	require.NoError(t, err)

	handler := func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/elsewhere", http.StatusFound) }
	srv := httptest.NewUnstartedServer(http.HandlerFunc(handler))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	srv.StartTLS()

	t.Cleanup(srv.Close)

	resp, err := get(t.Context(), api.NewPinnedHTTPClient(fingerprint, time.Second), srv.URL)
	require.ErrorIs(t, err, api.ErrUnexpectedRedirect)

	if resp != nil {
		require.NoError(t, resp.Body.Close())
	}
}

func TestRetryTransport_RoundTrip_RetryAfter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		retryAfter string
		want       time.Duration
	}{
		{"seconds", "3", 3 * time.Second},
		{"zero", "0", 0},
		{"past date", "Mon, 02 Jan 2006 15:04:05 GMT", 0},
		{"absent", "", 500 * time.Millisecond},
		{"unparseable", "soon", 500 * time.Millisecond},
		{"negative", "-1", 500 * time.Millisecond},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, url := startFakeServer(t, rateLimited(1, tc.retryAfter)...)
			waiter := &recordingWaiter{}

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
			require.NoError(t, err)

			require.Equal(t, http.StatusOK, send(t, waiter.transport(), req))
			require.Equal(t, []time.Duration{tc.want}, waiter.waits)
			require.Len(t, srv.received(), 2)
		})
	}
}

func TestRetryTransport_RoundTrip_UnreplayableBody(t *testing.T) {
	t.Parallel()

	srv, url := startFakeServer(t, rateLimited(1, "0")...)
	waiter := &recordingWaiter{}

	body := io.NopCloser(strings.NewReader(`{"x":1}`))

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, body)
	require.NoError(t, err)

	require.Equal(t, http.StatusTooManyRequests, send(t, waiter.transport(), req))
	require.Empty(t, waiter.waits)
	require.Len(t, srv.received(), 1)
}
