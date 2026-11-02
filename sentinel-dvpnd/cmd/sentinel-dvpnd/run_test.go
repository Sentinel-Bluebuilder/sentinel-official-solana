package main_test

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	main "github.com/sentinel-official/sentinel-dvpnd/cmd/sentinel-dvpnd"
	"github.com/sentinel-official/sentinel-dvpnd/internal/client"
	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-dvpnd/internal/daemon"
	"github.com/sentinel-official/sentinel-dvpnd/internal/worker"
)

type fakeServeRuntime struct {
	daemon.Runtime

	started *atomic.Bool
}

func (fakeServeRuntime) Jobs() []worker.Job { return nil }

func (fakeServeRuntime) Register(grpc.ServiceRegistrar) {}

func (fakeServeRuntime) Serve(ctx context.Context) error {
	<-ctx.Done()

	return nil
}

func (r fakeServeRuntime) Start(context.Context) error {
	r.started.Store(true)

	return nil
}

func (fakeServeRuntime) Stop(context.Context) error { return nil }

func TestPrepareRuntime_DNSCleanupFailureStopsBeforePreflight(t *testing.T) {
	t.Parallel()

	cause := errors.New("resolver restore failed")
	rt := fakeRecoverRuntime{recoverErr: errors.Join(client.ErrDNSCleanupFailed, cause)}

	err := main.PrepareRuntime(t.Context(), rt)
	require.ErrorIs(t, err, client.ErrDNSCleanupFailed)
	require.ErrorIs(t, err, cause)
}

func TestPrepareRuntime_ServiceCleanupFailureStopsBeforePreflight(t *testing.T) {
	t.Parallel()

	cause := errors.New("tunnel cleanup failed")
	rt := fakeRecoverRuntime{recoverErr: errors.Join(client.ErrServiceCleanupFailed, cause)}

	err := main.PrepareRuntime(t.Context(), rt)
	require.ErrorIs(t, err, client.ErrServiceCleanupFailed)
	require.ErrorIs(t, err, cause)
}

func TestServe_MetricsBindFailureFailsBeforeReady(t *testing.T) {
	t.Parallel()

	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })

	loaded := &config.Loaded{Dir: t.TempDir()}
	loaded.File.Metrics = config.Metrics{
		Enabled: true,
		Listen:  listener.Addr().String(),
	}

	var ready, started atomic.Bool

	err = main.Serve(t.Context(), loaded, fakeServeRuntime{started: &started}, func() { ready.Store(true) })

	var bindErr *net.OpError
	require.ErrorAs(t, err, &bindErr)
	require.Equal(t, "listen", bindErr.Op)
	require.False(t, started.Load())
	require.False(t, ready.Load())
}
