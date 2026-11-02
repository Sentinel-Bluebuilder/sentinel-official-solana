package daemon_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/sentinel-official/sentinel-dvpnd/internal/daemon"
	"github.com/sentinel-official/sentinel-dvpnd/internal/worker"
)

func TestLock_ExcludesSecondHolder(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	release, err := daemon.Lock(dir)
	require.NoError(t, err)

	_, err = daemon.Lock(dir)
	require.ErrorIs(t, err, daemon.ErrInstanceLocked)

	require.NoError(t, release())

	release2, err := daemon.Lock(dir)
	require.NoError(t, err)
	require.NoError(t, release2())
}

func TestLocked_ReportsHolder(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	locked, err := daemon.Locked(dir)
	require.NoError(t, err)
	require.False(t, locked)

	release, err := daemon.Lock(dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, release()) })

	locked, err = daemon.Locked(dir)
	require.NoError(t, err)
	require.True(t, locked)
}

func TestRun_CancelBeforeReady(t *testing.T) {
	t.Parallel()

	auxErr := errors.New("auxiliary task failed")

	for _, tc := range []struct {
		name       string
		preCancel  bool
		startAsync bool
	}{
		{name: "already canceled", preCancel: true},
		{name: "auxiliary failure during start", startAsync: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rt := &fakeRuntime{startEntered: make(chan struct{}), stopped: make(chan struct{})}
			opt := runOptions{preCancel: tc.preCancel}

			if tc.startAsync {
				rt.startCanceled = true
				opt.aux = func(context.Context) error {
					<-rt.startEntered

					return auxErr
				}
			}

			_, done, _, ready := startRun(t, rt, opt)
			err := waitRun(t, done)

			if tc.preCancel {
				require.ErrorIs(t, err, context.Canceled)
			} else {
				require.ErrorIs(t, err, auxErr)
			}

			select {
			case <-ready:
				t.Fatal("ready callback called after cancellation")
			default:
			}

			select {
			case <-rt.stopped:
				require.False(t, tc.preCancel, "runtime started after cancellation")
			default:
				require.True(t, tc.preCancel, "started runtime was not stopped")
			}
		})
	}
}

func TestRun_JobRegistrationFailureDoesNotSignalReady(t *testing.T) {
	t.Parallel()

	_, done, _, ready := startRun(t, &fakeRuntime{jobs: []worker.Job{{Name: "invalid"}}})
	require.Error(t, waitRun(t, done))

	select {
	case <-ready:
		t.Fatal("ready callback called after job registration failed")
	default:
	}
}

func TestRun_RuntimeExitIsFatal(t *testing.T) {
	t.Parallel()

	_, done, cancel, _ := startRun(t, &fakeRuntime{exit: true})
	require.ErrorIs(t, waitRun(t, done), daemon.ErrRuntimeExited)
	cancel()
}

func TestRun_StartFailureDoesNotSignalReady(t *testing.T) {
	t.Parallel()

	startErr := errors.New("start failed")
	_, done, _, ready := startRun(t, &fakeRuntime{startErr: startErr})
	require.ErrorIs(t, waitRun(t, done), startErr)

	select {
	case <-ready:
		t.Fatal("ready callback called after failed start")
	default:
	}
}

func TestRun_StopsOnCancel(t *testing.T) {
	t.Parallel()

	status := make(chan healthpb.HealthCheckResponse_ServingStatus, 1)
	addr, done, cancel, ready := startRun(t, &fakeRuntime{}, runOptions{onReady: func(addr string) {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			status <- healthpb.HealthCheckResponse_UNKNOWN

			return
		}
		defer func() { _ = conn.Close() }()

		ctx, stop := context.WithTimeout(t.Context(), runTimeout)
		defer stop()

		resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
		if err != nil {
			status <- healthpb.HealthCheckResponse_UNKNOWN

			return
		}

		status <- resp.GetStatus()
	}})

	select {
	case <-ready:
	case <-time.After(runTimeout):
		t.Fatal("ready callback not called")
	}

	require.Equal(t, healthpb.HealthCheckResponse_SERVING, <-status)
	waitServing(t, addr)

	cancel()
	require.NoError(t, waitRun(t, done))
}

func TestRun_StopsRuntimeAfterBlockedJob(t *testing.T) {
	t.Parallel()

	entered, release := make(chan struct{}), make(chan struct{})

	t.Cleanup(func() { close(release) })

	rt := &fakeRuntime{
		jobs: []worker.Job{{
			Interval: time.Hour,
			Name:     "blocked",
			Run: func(context.Context) error {
				close(entered)
				<-release

				return nil
			},
		}},
		stopErrs: make(chan error, 1),
	}
	_, done, cancel, _ := startRun(t, rt)

	select {
	case <-entered:
	case <-time.After(runTimeout):
		t.Fatal("job not started")
	}

	cancel()
	require.ErrorContains(t, waitRun(t, done), "shutting down scheduler")
	require.NoError(t, <-rt.stopErrs)
}
