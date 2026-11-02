package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

var (
	ErrBinaryNotFound  = errors.New("binary not found")
	ErrProcessExited   = errors.New("process exited")
	ErrProcessNotReady = errors.New("process not ready")
)

type Command struct {
	Args  []string
	Dir   string
	Env   []string
	Path  string
	Stdin io.Reader
	Unset []string
}

func LookPath(name string) (string, error) {
	if filepath.Base(name) != name {
		return name, nil
	}

	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}

	for _, dir := range slices.Concat(knownBinaryDirs(runtime.GOOS), registeredDirs()) {
		if path, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("%w: %s", ErrBinaryNotFound, name)
}

func PollReady(ctx context.Context, probe func(context.Context) error) error {
	return pollUntilReady(ctx, nil, nil, probe)
}

func Run(ctx context.Context, command Command) ([]byte, error) {
	var stdout, stderr bytes.Buffer

	cmd := newCmd(ctx, command)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("running %s: %w: %s", command.Path, err, bytes.TrimSpace(stderr.Bytes()))
	}

	return stdout.Bytes(), nil
}

func environWithout(env, names []string, goos string) []string {
	equal := func(a, b string) bool { return a == b }
	if goos == "windows" {
		equal = strings.EqualFold
	}

	return slices.DeleteFunc(slices.Clone(env), func(entry string) bool {
		key, _, _ := strings.Cut(entry, "=")

		return slices.ContainsFunc(names, func(name string) bool { return equal(key, name) })
	})
}

func exited(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func knownBinaryDirs(goos string) []string {
	switch goos {
	case "darwin":
		return []string{
			"/opt/homebrew/bin",
			"/opt/homebrew/sbin",
			"/usr/local/bin",
			"/usr/local/sbin",
			"/usr/sbin",
			"/sbin",
		}
	default:
		return nil
	}
}

func newCmd(ctx context.Context, command Command) *exec.Cmd {
	path := command.Path
	if resolved, err := LookPath(command.Path); err == nil {
		path = resolved
	}

	cmd := exec.CommandContext(ctx, path, command.Args...)
	cmd.Dir = command.Dir
	cmd.Env = append(environWithout(os.Environ(), command.Unset, runtime.GOOS), command.Env...)
	cmd.Stdin = command.Stdin
	configure(cmd)

	return cmd
}

func pollUntilReady(
	ctx context.Context,
	done <-chan struct{},
	exitErr func() error,
	probe func(context.Context) error,
) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(startTimeout)
	}

	probeCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	go func() {
		select {
		case <-done:
			cancel()
		case <-probeCtx.Done():
		}
	}()

	var lastErr error

	for probeCtx.Err() == nil {
		if exited(done) {
			return exitErr()
		}

		lastErr = probe(probeCtx)
		if lastErr == nil {
			if exited(done) {
				return exitErr()
			}

			return nil
		}

		select {
		case <-done:
			return exitErr()
		case <-time.After(pollInterval):
		case <-probeCtx.Done():
			if exited(done) {
				return exitErr()
			}

			if ctx.Err() != nil {
				return fmt.Errorf("waiting until ready: %w: %w", ctx.Err(), lastErr)
			}

			if ok {
				return fmt.Errorf("waiting until ready: %w: %w", probeCtx.Err(), lastErr)
			}

			return fmt.Errorf("%w: %w", ErrProcessNotReady, lastErr)
		}
	}

	if exited(done) {
		return exitErr()
	}

	if ok || ctx.Err() != nil {
		err := ctx.Err()
		if err == nil {
			err = context.DeadlineExceeded
		}

		if lastErr == nil {
			return fmt.Errorf("waiting until ready: %w", err)
		}

		return fmt.Errorf("waiting until ready: %w: %w", err, lastErr)
	}

	return fmt.Errorf("%w: %w", ErrProcessNotReady, lastErr)
}
