package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	metricsPath              = "/metrics"
	metricsReadHeaderTimeout = 5 * time.Second
	metricsStopTimeout       = 5 * time.Second
)

func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return reg
}

func ServeMetrics(ctx context.Context, listener net.Listener, reg *prometheus.Registry) error {
	mux := http.NewServeMux()
	mux.Handle(metricsPath, promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: metricsReadHeaderTimeout,
	}

	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serving metrics: %w", err)
		}

		return nil
	case <-ctx.Done():
	}

	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), metricsStopTimeout)
	defer cancel()

	if err := server.Shutdown(stopCtx); err != nil {
		return fmt.Errorf("shutting down metrics: %w", err)
	}

	return nil
}
