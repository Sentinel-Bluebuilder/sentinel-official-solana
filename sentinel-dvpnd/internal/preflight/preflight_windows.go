package preflight

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	filteringEngineProbeTimeout = 5 * time.Second
	filteringEngineService      = "BFE"
)

var errNotElevated = errors.New("insufficient privileges: the client role must run elevated")

func checkKillSwitchBackend(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", errKillSwitchBackendUnusable, err)
	}

	ctx, cancel := context.WithTimeout(ctx, filteringEngineProbeTimeout)
	defer cancel()

	result := make(chan error, 1)
	go func() { result <- probeFilteringEngine(ctx) }()

	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return fmt.Errorf("%w: checking the base filtering engine: %w", errKillSwitchBackendUnusable, ctx.Err())
	}
}

func checkPrivileges() error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return errNotElevated
	}

	return nil
}

func probeFilteringEngine(ctx context.Context) (result error) {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("%w: connecting to the service manager: %w", errKillSwitchBackendUnusable, err)
	}
	defer func() {
		if err := m.Disconnect(); err != nil {
			result = errors.Join(
				result,
				fmt.Errorf("%w: disconnecting from the service manager: %w", errKillSwitchBackendUnusable, err),
			)
		}
	}()

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", errKillSwitchBackendUnusable, err)
	}

	s, err := m.OpenService(filteringEngineService)
	if err != nil {
		return fmt.Errorf("%w: opening the base filtering engine service: %w", errKillSwitchBackendUnusable, err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf(
				"%w: closing the base filtering engine service: %w", errKillSwitchBackendUnusable, err,
			))
		}
	}()

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", errKillSwitchBackendUnusable, err)
	}

	status, err := s.Query()
	if err != nil {
		return fmt.Errorf("%w: querying the base filtering engine: %w", errKillSwitchBackendUnusable, err)
	}

	if status.State != svc.Running {
		return fmt.Errorf("%w: the base filtering engine is not running", errKillSwitchBackendUnusable)
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", errKillSwitchBackendUnusable, err)
	}

	return nil
}
