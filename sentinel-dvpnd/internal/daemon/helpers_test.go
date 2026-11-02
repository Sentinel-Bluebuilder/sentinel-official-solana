package daemon_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/sentinel-official/sentinel-dvpnd/internal/daemon"
	"github.com/sentinel-official/sentinel-dvpnd/internal/worker"
)

const (
	pollInterval = 10 * time.Millisecond
	runTimeout   = 5 * time.Second
)

type fakeRuntime struct {
	exit          bool
	jobs          []worker.Job
	startCanceled bool
	startEntered  chan struct{}
	startErr      error
	stopErrs      chan error
	stopped       chan struct{}
}

func (r *fakeRuntime) Jobs() []worker.Job { return r.jobs }

func (*fakeRuntime) Models() []any { return nil }

func (*fakeRuntime) Register(grpc.ServiceRegistrar) {}

func (r *fakeRuntime) Serve(ctx context.Context) error {
	if r.exit {
		return nil
	}

	<-ctx.Done()

	return nil
}

func (r *fakeRuntime) Start(ctx context.Context) error {
	if r.startEntered != nil {
		close(r.startEntered)
	}

	if r.startCanceled {
		<-ctx.Done()
	}

	return r.startErr
}

func (r *fakeRuntime) Stop(ctx context.Context) error {
	if r.stopped != nil {
		close(r.stopped)
	}

	if r.stopErrs != nil {
		r.stopErrs <- ctx.Err()
	}

	return nil
}

type runOptions struct {
	aux       func(context.Context) error
	onReady   func(string)
	preCancel bool
}

func startRun(
	t *testing.T,
	rt daemon.Runtime,
	options ...runOptions,
) (string, <-chan error, context.CancelFunc, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	var opt runOptions
	if len(options) != 0 {
		opt = options[0]
	}

	var listenConfig net.ListenConfig

	ln, err := listenConfig.Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	reg := prometheus.NewRegistry()
	server := daemon.NewServer(zerolog.Nop(), reg)
	runner, err := worker.New(zerolog.Nop(), reg)
	require.NoError(t, err)

	ready := make(chan struct{})
	done := make(chan error, 1)
	addr := ln.Addr().String()

	if opt.preCancel {
		cancel()
	}

	var aux []func(context.Context) error
	if opt.aux != nil {
		aux = append(aux, opt.aux)
	}

	go func() {
		done <- daemon.Run(ctx, rt, server, ln, runner, zerolog.Nop(), func() {
			if opt.onReady != nil {
				opt.onReady(addr)
			}

			close(ready)
		}, aux...)
	}()

	return addr, done, cancel, ready
}

func waitRun(t *testing.T, done <-chan error) error {
	t.Helper()

	select {
	case err := <-done:
		return err
	case <-time.After(runTimeout):
		t.Fatal("daemon did not stop")

		return nil
	}
}

func waitServing(t *testing.T, addr string) {
	t.Helper()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.Eventually(t, func() bool {
		ctx, cancel := context.WithTimeout(t.Context(), pollInterval)
		defer cancel()

		resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})

		return err == nil && resp.GetStatus() == healthpb.HealthCheckResponse_SERVING
	}, runTimeout, pollInterval)
}
