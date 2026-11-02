package main

import (
	"context"
	"io"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func ConfigureService(s installedService, cfg mgr.Config) error { return configureService(s, cfg) }

func ExecuteWindowsService(
	requests <-chan svc.ChangeRequest,
	status chan<- svc.Status,
	run func(context.Context, func()) error,
) (bool, uint32) {
	handler := serviceHandler{out: io.Discard, run: run}

	return handler.Execute(nil, requests, status)
}

func RollbackService(s installedService, cause error) error { return rollbackService(s, cause) }

func ServiceConfig(registration SCMRegistration, binary string) mgr.Config {
	return serviceConfig(registration, binary)
}

func ServiceCreateConfig(registration SCMRegistration) mgr.Config {
	return serviceCreateConfig(registration)
}

func StopService(ctx context.Context, s controlledService) error { return stopService(ctx, s) }
