//go:build linux

package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/rs/zerolog"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

const (
	hnsdBinary       = "hnsd"
	hnsdDirMode      = 0o700
	hnsdResolverAddr = "127.0.0.1:5350"
)

func runHNSD(ctx context.Context, dir string, peers uint, log zerolog.Logger) error {
	if err := os.MkdirAll(dir, hnsdDirMode); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	cmd := service.Command{
		Args: []string{"-p", strconv.FormatUint(uint64(peers), 10), "-r", hnsdResolverAddr, "-x", dir},
		Path: hnsdBinary,
	}

	p, err := service.StartProcess(ctx, cmd, log)
	if err != nil {
		return fmt.Errorf("starting: %w", err)
	}

	err = p.Wait(ctx)

	switch {
	case err != nil && errors.Is(err, context.Canceled):
		return nil
	case err != nil:
		return fmt.Errorf("running: %w", err)
	case ctx.Err() != nil:
		return nil //nolint:nilerr
	default:
		return service.ErrProcessExited
	}
}
