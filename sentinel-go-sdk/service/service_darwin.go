package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

const linkStatsTimeout = 5 * time.Second

//nolint:gochecknoinits
func init() {
	readProcessArgs = processArgsForPID
}

func LinkStats(iface string) (PeerStats, error) {
	ctx, cancel := context.WithTimeout(context.Background(), linkStatsTimeout)
	defer cancel()

	out, err := Run(ctx, Command{
		Args: []string{"-ibn"},
		Path: "netstat",
	})
	if err != nil {
		return PeerStats{}, fmt.Errorf("reading %s statistics: %w", iface, err)
	}

	return parseNetstat(out, iface)
}

func processArgsForPID(ctx context.Context, pid int) (string, []string, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, fmt.Errorf("checking process context: %w", err)
	}

	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", nil, fmt.Errorf("reading process arguments: %w", err)
	}

	if len(raw) < 6 {
		return "", nil, os.ErrInvalid
	}

	argc := int(binary.NativeEndian.Uint32(raw[:4]))
	if argc < 1 || argc > 1024 {
		return "", nil, os.ErrInvalid
	}

	raw = raw[4:]

	end := bytes.IndexByte(raw, 0)
	if end < 1 {
		return "", nil, os.ErrInvalid
	}

	executable := string(raw[:end])
	raw = bytes.TrimLeft(raw[end+1:], "\x00")

	args := make([]string, 0, argc)
	for range argc {
		end = bytes.IndexByte(raw, 0)
		if end < 1 {
			return "", nil, os.ErrInvalid
		}

		args = append(args, string(raw[:end]))
		raw = raw[end+1:]
	}

	return executable, args, nil
}
