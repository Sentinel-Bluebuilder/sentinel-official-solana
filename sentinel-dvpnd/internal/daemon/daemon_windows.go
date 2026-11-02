package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const (
	lockFlags        = windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY
	pipeClientRights = 0x120183
	pipeDescriptor   = "D:P(A;;GA;;;SY)(A;;GA;;;BA)"
)

func Listen(ctx context.Context, endpoint, group string) (net.Listener, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("starting daemon listener: %w", err)
	}

	descriptor, err := pipeSecurity(group)
	if err != nil {
		return nil, err
	}

	ln, err := winio.ListenPipe(endpoint, &winio.PipeConfig{SecurityDescriptor: descriptor})
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, errEndpointInUse
	}

	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", endpoint, err)
	}

	return ln, nil
}

func Lock(dir string) (func() error, error) {
	path := lockPath(dir)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, lockFileMode) //#nosec G304 -- Daemon lock file.
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}

	handle := windows.Handle(file.Fd())

	var overlapped windows.Overlapped
	if err := windows.LockFileEx(handle, lockFlags, 0, 1, 0, &overlapped); err != nil {
		_ = file.Close()

		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrInstanceLocked
		}

		return nil, fmt.Errorf("locking %s: %w", path, err)
	}

	release := func() error {
		unlockErr := windows.UnlockFileEx(handle, 0, 1, 0, &overlapped)
		closeErr := file.Close()

		return errors.Join(unlockErr, closeErr)
	}

	return release, nil
}

func Locked(dir string) (bool, error) {
	path := lockPath(dir)

	file, err := os.Open(path) //#nosec G304 -- Daemon lock file.
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("opening %s: %w", path, err)
	}

	handle := windows.Handle(file.Fd())

	var overlapped windows.Overlapped
	if err := windows.LockFileEx(handle, lockFlags, 0, 1, 0, &overlapped); err != nil {
		_ = file.Close()

		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return true, nil
		}

		return false, fmt.Errorf("probing %s: %w", path, err)
	}

	unlockErr := windows.UnlockFileEx(handle, 0, 1, 0, &overlapped)

	closeErr := file.Close()
	if err := errors.Join(unlockErr, closeErr); err != nil {
		return false, fmt.Errorf("releasing probe %s: %w", path, err)
	}

	return false, nil
}

func pipeDescriptorForSID(sid string) string {
	return fmt.Sprintf("%s(A;;0x%x;;;%s)", pipeDescriptor, pipeClientRights, sid)
}

func pipeSecurity(group string) (string, error) {
	if group == "" {
		return pipeDescriptor, nil
	}

	sid, _, _, err := windows.LookupSID("", group)
	if err != nil {
		return "", fmt.Errorf("looking up group %s: %w", group, err)
	}

	return pipeDescriptorForSID(sid.String()), nil
}
