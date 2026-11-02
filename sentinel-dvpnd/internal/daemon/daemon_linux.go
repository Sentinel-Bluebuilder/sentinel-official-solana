package daemon

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"
)

const (
	ofdGetLock = 36
	ofdSetLock = 37
)

type fcntlLock func(fd uintptr, cmd int, lk *syscall.Flock_t) error

func Lock(dir string) (func() error, error) {
	return lock(dir, syscall.FcntlFlock)
}

func Locked(dir string) (bool, error) {
	return locked(dir, syscall.FcntlFlock)
}

func lock(dir string, ofd fcntlLock) (func() error, error) {
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

	held := wholeFileLock(syscall.F_WRLCK)
	if err := ofd(file.Fd(), ofdSetLock, &held); err != nil && !ofdUnsupported(err) {
		_ = file.Close()

		return nil, fmt.Errorf("locking %s: %w", path, err)
	}

	release := func() error {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)

		return file.Close()
	}

	return release, nil
}

func locked(dir string, ofd fcntlLock) (bool, error) {
	path := lockPath(dir)

	file, err := os.Open(path) //#nosec G304 -- Daemon lock file.
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("opening %s: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	query := wholeFileLock(syscall.F_RDLCK)
	if err := ofd(file.Fd(), ofdGetLock, &query); err != nil {
		return false, fmt.Errorf("probing %s: %w", path, err)
	}

	return query.Type != syscall.F_UNLCK, nil
}

func ofdUnsupported(err error) bool {
	return errors.Is(err, syscall.ENOLCK) || errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.EOPNOTSUPP)
}

func wholeFileLock(kind int16) syscall.Flock_t {
	return syscall.Flock_t{
		Type:   kind,
		Whence: io.SeekStart,
	}
}
