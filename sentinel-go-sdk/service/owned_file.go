package service

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
)

const ManagedMarker = "Managed by " + instance.DaemonName

const maxMarkerLineBytes = int64(len("// " + ManagedMarker + "\n"))

var ErrConfigNotOwned = errors.New("config exists and was not created by " + instance.DaemonName)

func CopyOwned(src, dst, comment string) error {
	raw, err := os.ReadFile(src) //#nosec G304
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}

	return WriteOwned(dst, raw, comment)
}

func OwnsFile(path string) (bool, error) {
	f, err := openRegular(path, os.O_RDONLY)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, ErrConfigNotOwned) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	defer func() {
		_ = f.Close()
	}()

	return hasManagedMarker(f)
}

func RemoveOwned(dir, base string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}

	confOwned, err := OwnsFile(filepath.Join(dir, base+".conf"))
	if err != nil {
		return err
	}

	var errs []error

	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), base+".") {
			continue
		}

		path := filepath.Join(dir, entry.Name())

		owned := confOwned && entry.Name() == base+".auth.txt"
		if !owned {
			if owned, err = OwnsFile(path); err != nil {
				errs = append(errs, fmt.Errorf("removing %s: %w", path, err))

				continue
			}
		}

		if !owned {
			continue
		}

		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("removing %s: %w", path, err))
		}
	}

	return errors.Join(errs...)
}

func WriteOwned(path string, data []byte, comment string) (result error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	f, err := openRegular(path, os.O_RDWR)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	created := err != nil
	if created {
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //#nosec G304
		if err != nil {
			return fmt.Errorf("creating %s: %w", path, err)
		}
	}

	defer func() {
		if err := f.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("closing %s: %w", path, err))
		}
	}()

	if !created && comment != "" {
		owned, err := hasManagedMarker(f)
		if err != nil {
			return err
		}

		if !owned {
			return fmt.Errorf("%w: %s", ErrConfigNotOwned, path)
		}
	}

	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("truncating %s: %w", path, err)
	}

	if comment != "" {
		data = slices.Concat([]byte(comment+" "+ManagedMarker+"\n"), data)
	}

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

func hasManagedMarker(f *os.File) (bool, error) {
	line, err := bufio.NewReader(io.NewSectionReader(f, 0, maxMarkerLineBytes)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("reading %s: %w", f.Name(), err)
	}

	return line == "# "+ManagedMarker+"\n" || line == "// "+ManagedMarker+"\n", nil
}

func openRegular(path string, flag int) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("checking %s: %w", path, err)
	}

	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrConfigNotOwned, path)
	}

	f, err := os.OpenFile(path, flag, 0) //#nosec G304
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}

	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		_ = f.Close()

		return nil, errors.Join(fmt.Errorf("%w: %s changed while opening it", ErrConfigNotOwned, path), err)
	}

	return f, nil
}
