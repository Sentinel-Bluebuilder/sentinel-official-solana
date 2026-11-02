package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	grpchealth "google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"

	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
)

const (
	keepaliveInterval = 30 * time.Second
	keepaliveMinTime  = 5 * time.Second
	keepaliveTimeout  = 10 * time.Second
)

type Server struct {
	grpc   *grpc.Server
	health *grpchealth.Server
}

func NewServer(log zerolog.Logger, reg prometheus.Registerer) *Server {
	metrics := grpcprom.NewServerMetrics()
	reg.MustRegister(metrics)

	logEvent := func(_ context.Context, level logging.Level, msg string, fields ...any) {
		log.WithLevel(toZerologLevel(level)).Fields(fields).Msg(msg)
	}
	logger := logging.LoggerFunc(logEvent)
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			metrics.UnaryServerInterceptor(),
			logging.UnaryServerInterceptor(logger),
			recovery.UnaryServerInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			metrics.StreamServerInterceptor(),
			logging.StreamServerInterceptor(logger),
			recovery.StreamServerInterceptor(),
		),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             keepaliveMinTime,
			PermitWithoutStream: true,
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    keepaliveInterval,
			Timeout: keepaliveTimeout,
		}),
	)
	health := grpchealth.NewServer()
	health.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	healthpb.RegisterHealthServer(grpcServer, health)
	reflection.Register(grpcServer)
	daemonv1.RegisterDaemonServiceServer(grpcServer, service{})

	return &Server{
		grpc:   grpcServer,
		health: health,
	}
}

func (s *Server) markNotServing() {
	s.health.Shutdown()
}

func (s *Server) markServing() {
	for name := range s.grpc.GetServiceInfo() {
		s.health.SetServingStatus(name, healthpb.HealthCheckResponse_SERVING)
	}

	s.health.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
}

func (s *Server) serve(ln net.Listener) error {
	if err := s.grpc.Serve(ln); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("serving grpc: %w", err)
	}

	return nil
}

func (s *Server) stopWhenDone(ctx context.Context) {
	<-ctx.Done()

	stopped := make(chan struct{})

	go func() {
		s.grpc.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(grpcStopTimeout):
		s.grpc.Stop()
	}
}

func toZerologLevel(level logging.Level) zerolog.Level {
	switch level {
	case logging.LevelDebug:
		return zerolog.DebugLevel
	case logging.LevelInfo:
		return zerolog.InfoLevel
	case logging.LevelWarn:
		return zerolog.WarnLevel
	case logging.LevelError:
		return zerolog.ErrorLevel
	default:
		return zerolog.InfoLevel
	}
}
