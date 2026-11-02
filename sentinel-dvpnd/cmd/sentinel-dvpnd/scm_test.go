package main_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	main "github.com/sentinel-official/sentinel-dvpnd/cmd/sentinel-dvpnd"
)

type serviceFixture struct {
	commands chan main.ServiceCommand
	done     chan error
	states   chan main.ServiceState
}

func newServiceFixture(
	t *testing.T,
	run func(context.Context, func()) error,
	startTimeout, stopTimeout time.Duration,
) serviceFixture {
	t.Helper()

	fixture := serviceFixture{
		commands: make(chan main.ServiceCommand),
		done:     make(chan error, 1),
		states:   make(chan main.ServiceState, 8),
	}

	lifecycle := main.NewServiceLifecycleForTest(
		func(state main.ServiceState) { fixture.states <- state },
		run,
		startTimeout,
		stopTimeout,
	)
	go func() { fixture.done <- lifecycle.Execute(fixture.commands) }()

	return fixture
}

func (f serviceFixture) result(t *testing.T) error {
	t.Helper()

	select {
	case err := <-f.done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for service exit")
	}

	return nil
}

func (f serviceFixture) state(t *testing.T) main.ServiceState {
	t.Helper()

	select {
	case state := <-f.states:
		return state
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for service status")
	}

	return 0
}

func TestExitCode(t *testing.T) {
	t.Parallel()
	require.Zero(t, main.ExitCode(nil))
	require.Zero(t, main.ExitCode(context.Canceled))
	require.EqualValues(t, 1, main.ExitCode(errors.New("boom")))
	require.EqualValues(t, 1, main.ExitCode(errors.Join(context.Canceled, errors.New("cleanup failed"))))
	require.EqualValues(t, 1, main.ExitCode(errors.Join(context.Canceled, context.DeadlineExceeded)))
}

func TestNewSCMRegistration(t *testing.T) {
	t.Parallel()

	want := main.NewSCMRegistrationForTest(
		[]string{"run", "--instance", "c0"},
		"Sentinel dVPN daemon (c0)",
		"sentinel-dvpnd$c0",
	)
	require.Equal(t, want, main.NewSCMRegistration("c0"))
}

func TestServiceLifecycle_Execute_ClosedRequests(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t, func(ctx context.Context, _ func()) error {
		<-ctx.Done()

		return ctx.Err()
	}, time.Second, time.Second)
	require.Equal(t, main.ServiceStateStarting, fixture.state(t))
	close(fixture.commands)
	require.Equal(t, main.ServiceStateStopping, fixture.state(t))
	require.ErrorIs(t, fixture.result(t), context.Canceled)
}

func TestServiceLifecycle_Execute_Deadlines(t *testing.T) {
	t.Parallel()

	for _, startup := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "start"}[startup], func(t *testing.T) {
			t.Parallel()

			release := make(chan struct{})
			exited := make(chan struct{})

			t.Cleanup(func() { close(release); <-exited })

			startTimeout := time.Second
			if startup {
				startTimeout = 20 * time.Millisecond
			}

			fixture := newServiceFixture(t, func(ctx context.Context, ready func()) error {
				defer close(exited)

				if !startup {
					ready()
				}

				<-release

				return ctx.Err()
			}, startTimeout, 20*time.Millisecond)
			require.Equal(t, main.ServiceStateStarting, fixture.state(t))

			if !startup {
				require.Equal(t, main.ServiceStateRunning, fixture.state(t))

				fixture.commands <- main.ServiceCommandStop
			}

			require.Equal(t, main.ServiceStateStopping, fixture.state(t))
			require.ErrorIs(t, fixture.result(t), context.DeadlineExceeded)
		})
	}
}

func TestServiceLifecycle_Execute_FailedStart(t *testing.T) {
	t.Parallel()

	failure := errors.New("startup failed")
	fixture := newServiceFixture(t, func(context.Context, func()) error { return failure }, time.Second, time.Second)
	require.Equal(t, main.ServiceStateStarting, fixture.state(t))
	require.ErrorIs(t, fixture.result(t), failure)
	require.Empty(t, fixture.states)
}

func TestServiceLifecycle_Execute_ReadyAndStop(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t, func(ctx context.Context, ready func()) error {
		ready()
		<-ctx.Done()

		return ctx.Err()
	}, time.Second, time.Second)
	require.Equal(t, main.ServiceStateStarting, fixture.state(t))
	require.Equal(t, main.ServiceStateRunning, fixture.state(t))

	fixture.commands <- main.ServiceCommandInterrogate

	require.Equal(t, main.ServiceStateRunning, fixture.state(t))

	fixture.commands <- main.ServiceCommandStop

	require.Equal(t, main.ServiceStateStopping, fixture.state(t))
	require.ErrorIs(t, fixture.result(t), context.Canceled)
}

func TestServiceLifecycle_Execute_StopBeforeReady(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t, func(ctx context.Context, ready func()) error {
		<-ctx.Done()
		ready()

		return ctx.Err()
	}, time.Second, time.Second)
	require.Equal(t, main.ServiceStateStarting, fixture.state(t))

	fixture.commands <- main.ServiceCommandStop

	require.Equal(t, main.ServiceStateStopping, fixture.state(t))
	require.ErrorIs(t, fixture.result(t), context.Canceled)
	require.Empty(t, fixture.states)
}
