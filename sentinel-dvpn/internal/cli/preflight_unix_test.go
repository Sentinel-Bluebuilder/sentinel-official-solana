//go:build unix

package cli_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpn/internal/cli"
)

func TestPreflightEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("daemon down", func(t *testing.T) {
		t.Parallel()

		err := cli.PreflightEndpoint(context.Background(), filepath.Join(t.TempDir(), "daemon.sock"))
		require.ErrorIs(t, err, cli.ErrDaemonNotRunning)
	})

	t.Run("daemon up", func(t *testing.T) {
		t.Parallel()

		sock := filepath.Join(t.TempDir(), "daemon.sock")
		l, err := net.Listen("unix", sock)
		require.NoError(t, err)
		defer func() { _ = l.Close() }()

		require.NoError(t, cli.PreflightEndpoint(context.Background(), sock))
	})
}
