package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
)

const (
	serviceCommandIgnore serviceCommand = iota
	serviceCommandInterrogate
	serviceCommandStop
)

const (
	serviceStateStarting serviceState = iota
	serviceStateRunning
	serviceStateStopping
)

type scmRegistration struct {
	args        []string
	displayName string
	name        string
}

func newSCMRegistration(tag instance.Tag) scmRegistration {
	return scmRegistration{
		args:        []string{"run", "--instance", string(tag)},
		displayName: "Sentinel dVPN daemon (" + string(tag) + ")",
		name:        tag.ServiceName("windows"),
	}
}

type serviceCommand uint8

type serviceLifecycle[R any] struct {
	command      func(R) serviceCommand
	report       func(serviceState)
	run          func(context.Context, func()) error
	startTimeout time.Duration
	stopTimeout  time.Duration
}

func (s serviceLifecycle[R]) execute(requests <-chan R) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	state := serviceStateStarting
	s.report(state)

	ready := make(chan chan struct{})
	done := make(chan error, 1)

	go s.start(ctx, ready, done)

	timer := time.NewTimer(s.startTimeout)
	defer timer.Stop()

	deadline := timer.C

	var failure error

	stop := func() {
		if state == serviceStateStopping {
			return
		}

		cancel()

		state = serviceStateStopping
		s.report(state)
		timer.Reset(s.stopTimeout)
		deadline = timer.C
	}

	for {
		select {
		case err := <-done:
			return errors.Join(failure, err)
		case ack := <-ready:
			if state == serviceStateStarting {
				state = serviceStateRunning

				timer.Stop()

				deadline = nil

				s.report(state)
			}

			close(ack)
		case req, ok := <-requests:
			if !ok {
				requests = nil

				stop()

				continue
			}

			switch s.command(req) {
			case serviceCommandInterrogate:
				s.report(state)
			case serviceCommandStop:
				stop()
			case serviceCommandIgnore:
			}
		case <-deadline:
			if state == serviceStateStopping {
				return errors.Join(failure, fmt.Errorf("service shutdown: %w", context.DeadlineExceeded))
			}

			failure = fmt.Errorf("service startup: %w", context.DeadlineExceeded)

			stop()
		}
	}
}

func (s serviceLifecycle[R]) start(ctx context.Context, ready chan<- chan struct{}, done chan<- error) {
	var once sync.Once
	done <- s.run(ctx, func() {
		once.Do(func() {
			acknowledged := make(chan struct{})
			select {
			case ready <- acknowledged:
				select {
				case <-acknowledged:
				case <-ctx.Done():
				}
			case <-ctx.Done():
			}
		})
	})
}

type serviceState uint8

func exitCode(err error) uint32 {
	if err == nil {
		return 0
	}

	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, cause := range joined.Unwrap() {
			if exitCode(cause) != 0 {
				return 1
			}
		}

		return 0
	}

	if cause := errors.Unwrap(err); cause != nil {
		return exitCode(cause)
	}

	if errors.Is(err, context.Canceled) {
		return 0
	}

	return 1
}
