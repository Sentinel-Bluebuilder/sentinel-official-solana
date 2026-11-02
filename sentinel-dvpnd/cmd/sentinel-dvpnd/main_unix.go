//go:build unix

package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/spf13/pflag"

	"github.com/sentinel-official/sentinel-dvpnd/internal/daemon"
)

const privateUmask = 0o077

//nolint:gochecknoinits
func init() {
	syscall.Umask(privateUmask)
}

func fileGroupID(info fs.FileInfo) (int, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}

	return int(stat.Gid), true
}

func prepareInstanceDir(dir, group string) error {
	if err := os.MkdirAll(dir, instanceDirMode); err != nil {
		return fmt.Errorf("creating instance folder: %w", err)
	}

	if err := os.Chmod(dir, instanceDirMode); err != nil { //#nosec G302 -- The instance folder is group-readable.
		return fmt.Errorf("setting instance folder mode: %w", err)
	}

	if group != "" {
		found, err := user.LookupGroup(group)
		if err != nil {
			return fmt.Errorf("looking up group: %w", err)
		}

		root := filepath.Dir(dir)
		if err := os.Chmod(root, instanceDirMode); err != nil { //#nosec G302 -- The state root is group-readable by design.
			return fmt.Errorf("setting state root mode: %w", err)
		}

		gid, _ := strconv.Atoi(found.Gid)

		for _, path := range []string{root, dir} {
			if err := os.Chown(path, -1, gid); err != nil {
				return fmt.Errorf("setting %s group: %w", path, err)
			}
		}
	}

	return nil
}

func runDaemon(ctx context.Context, opts *instanceOptions, flags *pflag.FlagSet) error {
	return runInstance(ctx, opts, flags, os.Stdout, daemon.NotifyReady())
}
