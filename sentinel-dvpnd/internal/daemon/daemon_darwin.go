package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

func Lock(dir string) (func() error, error) {
	path := lockPath(dir)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, lockFileMode) //#nosec G304 -- Daemon lock file.
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()

		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrInstanceLocked
		}

		return nil, fmt.Errorf("locking %s: %w", path, err)
	}

	release := func() error {
		unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
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

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()

		if errors.Is(err, syscall.EWOULDBLOCK) {
			return true, nil
		}

		return false, fmt.Errorf("probing %s: %w", path, err)
	}

	unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)

	closeErr := file.Close()
	if err := errors.Join(unlockErr, closeErr); err != nil {
		return false, fmt.Errorf("releasing probe %s: %w", path, err)
	}

	return false, nil
}
