package apiserver

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/rs/zerolog"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
)

const (
	resultError  = "error"
	resultOK     = "ok"
	unknownRoute = "unknown"
)

type Server struct {
	cert        tls.Certificate
	engine      *gin.Engine
	fingerprint string
	port        uint16
	requests    *prometheus.CounterVec
}

func (s *Server) Engine() *gin.Engine { return s.engine }

func (s *Server) Fingerprint() string { return s.fingerprint }

func (s *Server) Serve(ctx context.Context) error {
	if err := api.Serve(ctx, fmt.Sprintf(":%d", s.port), s.cert, s.engine); err != nil {
		return fmt.Errorf("serving api: %w", err)
	}

	return nil
}

func (s *Server) countAPIRequests() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.Next()

		route := ctx.FullPath()
		if route == "" {
			route = unknownRoute
		}

		result := resultOK
		if ctx.Writer.Status() >= http.StatusBadRequest {
			result = resultError
		}

		s.requests.WithLabelValues(route, result).Inc()
	}
}

func New(
	dir string,
	port uint16,
	log zerolog.Logger,
	reg prometheus.Registerer,
	streamingPaths []string,
) (*Server, error) {
	cert, fingerprint, err := api.LoadOrCreateServerCert(dir)
	if err != nil {
		return nil, fmt.Errorf("loading server certificate: %w", err)
	}

	requests := promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
		Help: "Total number of public API requests.",
		Name: "sentinel_dvpnd_api_requests_total",
	}, []string{"route", "result"})
	server := &Server{
		cert: cert,
		engine: api.NewEngine(api.EngineOptions{
			Logger:         log,
			StreamingPaths: streamingPaths,
		}),
		fingerprint: fingerprint,
		port:        port,
		requests:    requests,
	}
	server.engine.Use(server.countAPIRequests())

	return server, nil
}
