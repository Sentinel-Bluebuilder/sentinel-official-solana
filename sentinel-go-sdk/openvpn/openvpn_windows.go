//go:build windows

package openvpn

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	dcoDevicePath = `\\.\ovpn-dco`
	dcoServiceKey = `SYSTEM\CurrentControlSet\Services\ovpn-dco`
)

//nolint:gochecknoinits
func init() {
	lookupDirs = windowsLookupDirs
	detectDCO = windowsDetectDCO
}

func detectDCOService() (bool, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, dcoServiceKey, registry.QUERY_VALUE)

	switch {
	case err == nil:
		_ = key.Close()

		return true, nil
	case errors.Is(err, registry.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("opening %s: %w", dcoServiceKey, err)
	}
}

func windowsDetectDCO() (bool, error) {
	name, err := windows.UTF16PtrFromString(dcoDevicePath)
	if err != nil {
		return false, fmt.Errorf("encoding %s: %w", dcoDevicePath, err)
	}

	handle, err := windows.CreateFile(
		name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0,
	)

	switch {
	case err == nil:
		_ = windows.CloseHandle(handle)

		return true, nil
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return true, nil
	case errors.Is(err, windows.ERROR_FILE_NOT_FOUND):
		return detectDCOService()
	default:
		return false, fmt.Errorf("opening %s: %w", dcoDevicePath, err)
	}
}

func windowsLookupDirs() []string {
	programFiles := os.Getenv("ProgramFiles")
	if programFiles == "" {
		return nil
	}

	return []string{filepath.Join(programFiles, "OpenVPN", "bin")}
}
