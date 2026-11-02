package wgcore

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
)

//nolint:gochecknoinits
func init() {
	serviceInstalled = scmServiceInstalled
}

func scmServiceInstalled(name string) (bool, error) {
	m, err := mgr.Connect()
	if err != nil {
		return false, fmt.Errorf("connecting to the service manager: %w", err)
	}

	defer func() { _ = m.Disconnect() }()

	s, err := m.OpenService(name)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("opening service %s: %w", name, err)
	}

	_ = s.Close()

	return true, nil
}
