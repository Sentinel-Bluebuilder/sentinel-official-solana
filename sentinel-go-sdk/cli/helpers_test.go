package cli_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

type keyInfo struct {
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
	Sealed    bool   `json:"sealed"`
}

func decorate(t *testing.T, base string) string {
	t.Helper()

	parsed, err := url.Parse(base)
	require.NoError(t, err)

	parsed.User = url.UserPassword("user", "SECRETPASS")
	parsed.Path = "/SECRETPATH/"
	parsed.RawQuery = "api-key=SECRETQ"

	return parsed.String()
}

func refusedURL(t *testing.T) string {
	t.Helper()

	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	return "http://" + addr
}

func requireOnlyHost(t *testing.T, err error, host string) {
	t.Helper()

	require.Error(t, err)
	require.Contains(t, err.Error(), host)

	for _, secret := range []string{"SECRETPASS", "SECRETPATH", "SECRETQ"} {
		require.NotContains(t, err.Error(), secret)
	}
}

func serve(t *testing.T, status int, body string) string {
	t.Helper()

	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)

	return srv.URL
}
