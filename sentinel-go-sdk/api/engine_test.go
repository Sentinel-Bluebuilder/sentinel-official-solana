package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
)

func TestCopyContext(t *testing.T) {
	t.Parallel()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	var seen *gin.Context

	handler := strictHandler(func(c *gin.Context, _ any) (any, error) {
		seen = c

		return "ok", nil
	})

	wrapped := api.CopyContext(handler, "operation")

	out, err := wrapped(ctx, nil)
	require.NoError(t, err)
	require.Equal(t, "ok", out)
	require.NotNil(t, seen)
	require.NotSame(t, ctx, seen)
}

//nolint:paralleltest
func TestNewEngine_BodyAndRecovery(t *testing.T) {
	gin.SetMode(gin.TestMode)

	options := api.EngineOptions{
		Logger:       zerolog.Nop(),
		MaxBodyBytes: 16,
	}
	engine := api.NewEngine(options)

	echo := func(c *gin.Context) {
		var v map[string]any
		if err := c.ShouldBindJSON(&v); err != nil {
			api.Abort(c, err)

			return
		}

		c.JSON(http.StatusOK, v)
	}
	engine.POST("/echo", echo)

	panicHandler := func(*gin.Context) { panic("boom") }
	engine.GET("/panic", panicHandler)

	for _, chunked := range []bool{false, true} {
		big := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/echo",
			strings.NewReader(`{"a":"`+strings.Repeat("x", 100)+`"}`),
		)
		if chunked {
			big.ContentLength = -1
		}

		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, big)
		require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)

		decoder := json.NewDecoder(recorder.Body)

		var response api.ErrorResponse
		require.NoError(t, decoder.Decode(&response))
		require.Equal(t, api.CodeRequestTooLarge, response.Error.Code)
		require.ErrorIs(t, decoder.Decode(new(any)), io.EOF)
	}

	panicReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/panic", nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, panicReq)
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"code":"internal"`)

	okReq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/echo", strings.NewReader(`{"a":1}`))
	recorder = httptest.NewRecorder()
	engine.ServeHTTP(recorder, okReq)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotEmpty(t, recorder.Header().Get("X-Request-ID"))
}

//nolint:paralleltest
func TestNewEngine_DefaultBodyLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	engine := api.NewEngine(api.EngineOptions{Logger: zerolog.Nop()})
	engine.POST("/echo", func(c *gin.Context) {
		if _, err := io.ReadAll(c.Request.Body); err != nil {
			api.Abort(c, err)

			return
		}

		c.Status(http.StatusOK)
	})

	post := func(size int) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost, "/echo", strings.NewReader(strings.Repeat("x", size)),
		)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)

		return recorder
	}

	require.Equal(t, http.StatusOK, post(api.MaxRequestBodyBytes).Code)

	recorder := post(api.MaxRequestBodyBytes + 1)
	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	require.Contains(t, recorder.Body.String(), string(api.CodeRequestTooLarge))
}

func TestNewEngine_EmptyStreamingPathKeepsNotFoundTimeout(t *testing.T) {
	t.Parallel()

	engine := api.NewEngine(api.EngineOptions{
		Logger:         zerolog.Nop(),
		StreamingPaths: []string{""},
		Timeout:        20 * time.Millisecond,
	})
	engine.NoRoute(func(c *gin.Context) {
		<-time.After(60 * time.Millisecond)
		c.String(http.StatusNotFound, "too late")
	})

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/missing", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	require.Equal(t, http.StatusInternalServerError, response.Code)
}

func TestNewEngine_StreamingPathSkipsTimeout(t *testing.T) {
	t.Parallel()

	engine := api.NewEngine(api.EngineOptions{
		Logger:         zerolog.Nop(),
		StreamingPaths: []string{"/stream/:id"},
		Timeout:        20 * time.Millisecond,
	})
	engine.GET("/stream/:id", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.Flush()
		time.Sleep(60 * time.Millisecond)

		_, _ = c.Writer.WriteString("data: late\n\n")
		c.Writer.Flush()
	})
	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)

	resp, err := get(t.Context(), srv.Client(), srv.URL+"/stream/1")
	require.NoError(t, err)

	defer func() { require.NoError(t, resp.Body.Close()) }()

	body, err := io.ReadAll(resp.Body)

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "data: late\n\n", string(body))
}

//nolint:paralleltest
func TestNewEngine_Timeout(t *testing.T) {
	gin.SetMode(gin.TestMode)

	options := api.EngineOptions{
		Logger:         zerolog.Nop(),
		StreamingPaths: []string{"/stream/:id"},
		Timeout:        20 * time.Millisecond,
	}
	engine := api.NewEngine(options)

	slow := func(c *gin.Context) {
		time.Sleep(150 * time.Millisecond)
		c.String(http.StatusOK, "too late")
	}
	engine.GET("/slow", slow)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/slow", nil)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"code":"internal"`)
}

func TestNewEngine_UnknownRouteAndMethod(t *testing.T) {
	t.Parallel()

	engine := api.NewEngine(api.EngineOptions{Logger: zerolog.Nop()})
	engine.POST("/echo", func(c *gin.Context) { c.Status(http.StatusOK) })

	cases := []struct {
		method string
		path   string
		status int
		code   string
	}{
		{http.MethodGet, "/nope", http.StatusNotFound, "not_found"},
		{http.MethodDelete, "/echo", http.StatusMethodNotAllowed, "method_not_allowed"},
	}

	for _, tc := range cases {
		req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, nil)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)

		require.Equal(t, tc.status, recorder.Code)
		require.Contains(t, recorder.Body.String(), `"code":"`+tc.code+`"`)
	}
}

func TestServe(t *testing.T) {
	t.Parallel()

	cert, fingerprint, err := api.LoadOrCreateServerCert(t.TempDir())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	listenConfig := new(net.ListenConfig)
	listener, err := listenConfig.Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	handler := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	done := make(chan error, 1)
	serve := func() { done <- api.Serve(ctx, addr, cert, http.HandlerFunc(handler)) }

	go serve()

	client := api.NewPinnedHTTPClient(fingerprint, time.Second)
	requireServing(t, ctx, client, "https://"+addr)

	cancel()
	require.NoError(t, <-done)
}

func TestServe_CancelsOpenStreams(t *testing.T) {
	t.Parallel()

	cert, fingerprint, err := api.LoadOrCreateServerCert(t.TempDir())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	reqCtx, cancelReq := context.WithCancel(t.Context())

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	started := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			w.WriteHeader(http.StatusOK)

			return
		}

		close(started)
		<-r.Context().Done()
	})
	done := make(chan error, 1)

	served := make(chan struct{})
	go func() {
		defer close(served)

		done <- api.Serve(ctx, addr, cert, handler)
	}()

	defer func() {
		cancelReq()
		cancel()

		select {
		case <-served:
		case <-time.After(6 * time.Second):
			t.Error("server did not finish during cleanup")
		}
	}()

	client := api.NewPinnedHTTPClient(fingerprint, 10*time.Second)
	requireServing(t, ctx, client, "https://"+addr+"/ready")

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "https://"+addr+"/stream", nil)
	require.NoError(t, err)

	requestDone := make(chan error, 1)

	go func() {
		resp, err := client.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}

		requestDone <- err
	}()

	select {
	case <-started:
	case err := <-requestDone:
		t.Fatalf("stream request ended before shutdown: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("stream handler did not start")
	}

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop with an open stream")
	}

	cancelReq()

	select {
	case <-requestDone: //nolint:errcheck // only completion of the request is under test, not its error
	case <-time.After(2 * time.Second):
		t.Fatal("stream request did not finish")
	}
}

func TestServe_ClosesUnresponsiveStreamsAfterDeadline(t *testing.T) {
	t.Parallel()

	cert, fingerprint, err := api.LoadOrCreateServerCert(t.TempDir())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			w.WriteHeader(http.StatusOK)

			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-release
	})
	done := make(chan error, 1)

	served := make(chan struct{})
	go func() {
		defer close(served)

		done <- api.Serve(ctx, addr, cert, handler)
	}()

	defer func() {
		close(release)
		cancel()

		select {
		case <-served:
		case <-time.After(7 * time.Second):
			t.Error("server did not finish during cleanup")
		}
	}()

	client := api.NewPinnedHTTPClient(fingerprint, 10*time.Second)
	requireServing(t, ctx, client, "https://"+addr+"/ready")

	resp, err := get(t.Context(), client, "https://"+addr+"/stream")
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	readDone := make(chan error, 1)
	go func() {
		_, err := resp.Body.Read(make([]byte, 1))
		readDone <- err
	}()

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(7 * time.Second):
		t.Fatal("server did not stop after its shutdown deadline")
	}

	select {
	case err := <-readDone:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("unresponsive stream connection remained open")
	}
}

func requireServing(t *testing.T, ctx context.Context, client *http.Client, url string) {
	t.Helper()

	ready := func() bool {
		resp, err := get(ctx, client, url)
		if err != nil {
			return false
		}

		ok := resp.StatusCode == http.StatusOK
		_ = resp.Body.Close()

		return ok
	}
	require.Eventually(t, ready, 5*time.Second, 50*time.Millisecond)
}
