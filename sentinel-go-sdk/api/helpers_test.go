package api_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
)

type fakeResponse struct {
	body        string
	contentType string
	location    string
	retryAfter  string
	status      int
}

type fakeServer struct {
	bodies    [][]byte
	conns     atomic.Int32
	mu        sync.Mutex
	responses []fakeResponse
}

func (s *fakeServer) ServeHTTP(writer http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)

		return
	}

	s.mu.Lock()
	s.bodies = append(s.bodies, body)
	hit := len(s.bodies)
	s.mu.Unlock()

	if hit > len(s.responses) {
		writer.WriteHeader(http.StatusOK)

		return
	}

	resp := s.responses[hit-1]
	for key, value := range map[string]string{
		"Content-Type": resp.contentType,
		"Location":     resp.location,
		"Retry-After":  resp.retryAfter,
	} {
		if value != "" {
			writer.Header().Set(key, value)
		}
	}

	payload := resp.body
	if payload == "" {
		payload = http.StatusText(resp.status)
	}

	writer.WriteHeader(resp.status)
	_, _ = io.WriteString(writer, payload)
}

func (s *fakeServer) connState(_ net.Conn, state http.ConnState) {
	if state == http.StateNew {
		s.conns.Add(1)
	}
}

func (s *fakeServer) received() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.bodies)
}

type recordingWaiter struct {
	waits []time.Duration
}

func (r *recordingWaiter) transport() *api.RetryTransport {
	return api.NewRetryTransportForTest(http.DefaultTransport, r.wait)
}

func (r *recordingWaiter) wait(_ context.Context, delay time.Duration) error {
	r.waits = append(r.waits, delay)

	return nil
}

type strictHandler func(*gin.Context, any) (any, error)

func allows(limiter *api.Limiter, key string) bool {
	_, ok := limiter.Allow(key)

	return ok
}

func get(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("performing request: %w", err)
	}

	return resp, nil
}

func newLimitedEngine(middlewares ...gin.HandlerFunc) *gin.Engine {
	options := api.EngineOptions{Logger: zerolog.Nop()}
	engine := api.NewEngine(options)

	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	middlewares = append(middlewares, ok)
	engine.GET("/limited", middlewares...)

	return engine
}

func rateLimited(count int, retryAfter string) []fakeResponse {
	out := make([]fakeResponse, count)
	for i := range out {
		out[i] = fakeResponse{
			retryAfter: retryAfter,
			status:     http.StatusTooManyRequests,
		}
	}

	return out
}

func send(t *testing.T, transport http.RoundTripper, req *http.Request) int {
	t.Helper()

	client := &http.Client{Transport: transport}

	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	return resp.StatusCode
}

func serveLimited(t *testing.T, engine *gin.Engine, key solana.PrivateKey) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/limited", nil)

	if key != nil {
		sig, err := auth.SignRequest(key, http.MethodGet, "/limited", nil, time.Now())
		require.NoError(t, err)
		sig.Apply(req.Header)
	}

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	return recorder
}

func startFakeServer(t *testing.T, responses ...fakeResponse) (*fakeServer, string) {
	t.Helper()

	server := &fakeServer{responses: responses}

	srv := httptest.NewUnstartedServer(server)
	srv.Config.ConnState = server.connState
	srv.Start()
	t.Cleanup(srv.Close)

	return server, srv.URL
}
