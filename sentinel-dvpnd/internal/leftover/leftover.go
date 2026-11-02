package leftover

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"runtime"
	"slices"
	"strings"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

var ErrInterfaceExists = errors.New("leftover interface exists")

type InterfaceRemover interface {
	RemoveInterface(ctx context.Context, name string) error
}

type Remover struct {
	exec   execFunc
	exists func(name string) (bool, error)
	goos   string
}

func NewRemover() Remover {
	return Remover{
		exec:   service.Run,
		exists: interfaceExists,
		goos:   runtime.GOOS,
	}
}

func (r Remover) RemoveInterface(ctx context.Context, name string) error {
	if r.goos != "linux" {
		return nil
	}

	exists, err := r.exists(name)
	if err != nil {
		return err
	}

	if !exists {
		return nil
	}

	command := service.Command{
		Args: []string{"link", "delete", "dev", name},
		Path: "ip",
	}

	_, err = r.exec(ctx, command)
	if err == nil {
		return nil
	}

	exists, recheckErr := r.exists(name)
	if recheckErr == nil && !exists {
		return nil
	}

	return fmt.Errorf(`%w: %s; remove it with "ip link delete dev %[2]s": %w`, ErrInterfaceExists, name, err)
}

type execFunc func(ctx context.Context, command service.Command) ([]byte, error)

func RemoveCopies(dirs []string, base string) error {
	var errs []error

	for _, dir := range dirs {
		if err := service.RemoveOwned(dir, base); err != nil {
			errs = append(errs, fmt.Errorf("removing leftover copies in %s%s: %w", dir, removeHint(dir, err), err))
		}
	}

	return errors.Join(errs...)
}

func failedPaths(dir string, err error) []string {
	causes := []error{err}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes = joined.Unwrap()
	}

	var paths []string

	for _, cause := range causes {
		if pathErr, ok := errors.AsType[*fs.PathError](cause); ok && pathErr.Path != dir {
			paths = append(paths, pathErr.Path)
		}
	}

	return paths
}

func interfaceExists(name string) (bool, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return false, fmt.Errorf("looking up interface %s: %w", name, err)
	}

	return slices.ContainsFunc(interfaces, func(iface net.Interface) bool { return iface.Name == name }), nil
}

func removeHint(dir string, err error) string {
	paths := failedPaths(dir, err)
	if len(paths) == 0 {
		return ""
	}

	return fmt.Sprintf(`; remove the failed copies with "rm %s"`, strings.Join(paths, " "))
}
