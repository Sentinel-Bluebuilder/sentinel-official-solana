package api

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gin-contrib/logger"
	"github.com/gin-contrib/requestid"
	"github.com/gin-contrib/timeout"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"
)

const MaxRequestBodyBytes = 64 << 10

const (
	defaultTimeout  = 30 * time.Second
	shutdownTimeout = 5 * time.Second
)

//nolint:gochecknoglobals
var (
	requestIDHandler = sync.OnceValue(newRequestIDHandler)
	timeoutHandlers  sync.Map
)

type EngineOptions struct {
	Logger         zerolog.Logger
	MaxBodyBytes   int64
	StreamingPaths []string
	Timeout        time.Duration
}

type timeoutEntry struct {
	handler gin.HandlerFunc
	once    sync.Once
}

func CopyContext[H ~func(*gin.Context, any) (any, error)](handler H, _ string) H {
	copied := func(ctx *gin.Context, request any) (any, error) {
		return handler(ctx.Copy(), request)
	}

	return H(copied)
}

func NewEngine(options EngineOptions) *gin.Engine {
	if options.MaxBodyBytes == 0 {
		options.MaxBodyBytes = MaxRequestBodyBytes
	}

	if options.Timeout == 0 {
		options.Timeout = defaultTimeout
	}

	recovery := func(c *gin.Context, _ any) { Abort(c, ErrInternal) }
	withLogger := func(_ *gin.Context, _ zerolog.Logger) zerolog.Logger { return options.Logger }

	streamingPaths := make(map[string]bool, len(options.StreamingPaths))
	for _, path := range options.StreamingPaths {
		if path != "" {
			streamingPaths[path] = true
		}
	}

	boundedHandler := timeoutHandler(options.Timeout)
	skipStreamingTimeout := func(c *gin.Context) {
		if streamingPaths[c.FullPath()] {
			c.Next()

			return
		}

		boundedHandler(c)
	}

	engine := gin.New()
	engine.ContextWithFallback = true
	engine.HandleMethodNotAllowed = true
	engine.NoRoute(func(c *gin.Context) { Abort(c, ErrNotFound) })
	engine.NoMethod(func(c *gin.Context) { Abort(c, ErrMethodNotAllowed) })

	if err := engine.SetTrustedProxies(nil); err != nil {
		panic(err)
	}

	engine.Use(
		gin.CustomRecovery(recovery),
		requestIDHandler(),
		logger.SetLogger(logger.WithLogger(withLogger)),
		limitRequestBody(options.MaxBodyBytes),
		skipStreamingTimeout,
	)

	return engine
}

func Serve(ctx context.Context, addr string, cert tls.Certificate, handler http.Handler) error {
	bctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()

	srv := &http.Server{
		Addr:              addr,
		BaseContext:       func(net.Listener) context.Context { return bctx },
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		},
	}
	srv.RegisterOnShutdown(cancel)

	group, gctx := errgroup.WithContext(ctx)

	listen := func() error {
		if err := srv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serving %s: %w", addr, err)
		}

		return nil
	}
	shutdown := func() error {
		<-gctx.Done()

		sctx, scancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer scancel()

		if err := srv.Shutdown(sctx); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				if closeErr := srv.Close(); closeErr != nil {
					return fmt.Errorf("closing server: %w", closeErr)
				}

				return nil
			}

			return fmt.Errorf("shutting down server: %w", err)
		}

		return nil
	}

	group.Go(listen)
	group.Go(shutdown)

	if err := group.Wait(); err != nil {
		return fmt.Errorf("running server on %s: %w", addr, err)
	}

	return nil
}

func newRequestIDHandler() gin.HandlerFunc {
	return requestid.New()
}

func newTimeoutHandler(limit time.Duration) gin.HandlerFunc {
	onTimeout := func(c *gin.Context) { Abort(c, ErrInternal) }

	return timeout.New(timeout.WithTimeout(limit), timeout.WithResponse(onTimeout))
}

func timeoutEntryFor(limit time.Duration) *timeoutEntry {
	candidate := &timeoutEntry{}

	actual, _ := timeoutHandlers.LoadOrStore(limit, candidate)

	return actual.(*timeoutEntry)
}

func timeoutHandler(limit time.Duration) gin.HandlerFunc {
	entry := timeoutEntryFor(limit)

	build := func() { entry.handler = newTimeoutHandler(limit) }
	entry.once.Do(build)

	return entry.handler
}
