package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"runtime"
	"time"

	systemd "github.com/coreos/go-systemd/v22/daemon"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/sentinel-official/sentinel-dvpnd/internal/worker"
	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
	"github.com/sentinel-official/sentinel-go-sdk/v3/version"
)

const (
	grpcStopTimeout    = 30 * time.Second
	lockFileMode       = 0o600
	lockFileName       = "daemon.lock"
	runtimeStopTimeout = 10 * time.Second
)

var (
	ErrInstanceLocked = errors.New("instance folder is locked by another daemon")
	ErrRuntimeExited  = errors.New("runtime exited unexpectedly")
)

var errEndpointInUse = errors.New("a daemon is already listening on this endpoint")

type Ready func()

type Recoverer interface {
	Recover(ctx context.Context) error
}

type Runtime interface {
	Models() []any
	Register(registrar grpc.ServiceRegistrar)
	Start(ctx context.Context) error
	Jobs() []worker.Job
	Serve(ctx context.Context) error
	Stop(ctx context.Context) error
}

type service struct {
	daemonv1.UnimplementedDaemonServiceServer
}

func (service) GetVersion(context.Context, *daemonv1.GetVersionRequest) (*daemonv1.GetVersionResponse, error) {
	return &daemonv1.GetVersionResponse{Version: daemonv1.VersionFrom(version.Current())}, nil
}

func NotifyReady() Ready {
	if runtime.GOOS != "linux" {
		return func() {}
	}

	return func() { _, _ = systemd.SdNotify(false, systemd.SdNotifyReady) }
}

func Run(
	ctx context.Context,
	rt Runtime,
	server *Server,
	ln net.Listener,
	runner *worker.Runner,
	log zerolog.Logger,
	ready Ready,
	aux ...func(context.Context) error,
) error {
	rt.Register(server.grpc)

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error { return server.serve(ln) })
	group.Go(func() error {
		server.stopWhenDone(groupCtx)

		return nil
	})

	for _, run := range aux {
		group.Go(func() error { return run(groupCtx) })
	}

	group.Go(func() error { return runRuntime(groupCtx, rt, server, runner, log, ready) })

	return group.Wait() //nolint:wrapcheck // Errgroup preserves the runtime's typed error.
}

func lockPath(dir string) string { return filepath.Join(dir, lockFileName) }

func runRuntime(
	ctx context.Context,
	rt Runtime,
	server *Server,
	runner *worker.Runner,
	log zerolog.Logger,
	ready Ready,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("starting runtime: %w", err)
	}

	if err := rt.Start(ctx); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return errors.Join(err, stopRuntime(ctx, rt, runner))
	}

	if err := runner.Register(rt.Jobs()...); err != nil {
		return errors.Join(err, stopRuntime(ctx, rt, runner))
	}

	if err := ctx.Err(); err != nil {
		return errors.Join(err, stopRuntime(ctx, rt, runner))
	}

	runner.Start()

	if err := ctx.Err(); err != nil {
		return errors.Join(err, stopRuntime(ctx, rt, runner))
	}

	server.markServing()

	if err := ctx.Err(); err != nil {
		server.markNotServing()

		return errors.Join(err, stopRuntime(ctx, rt, runner))
	}

	ready()
	log.Info().Msg("ready")

	serveErr := rt.Serve(ctx)

	server.markNotServing()

	stopErr := stopRuntime(ctx, rt, runner)
	if serveErr == nil && ctx.Err() == nil {
		serveErr = ErrRuntimeExited
	}

	return errors.Join(serveErr, stopErr)
}

func stopRuntime(ctx context.Context, rt Runtime, runner *worker.Runner) error {
	runnerErr := runner.Stop()

	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runtimeStopTimeout)
	defer cancel()

	return errors.Join(runnerErr, rt.Stop(stopCtx))
}
