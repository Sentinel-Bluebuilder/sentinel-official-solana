package daemon_test

import (
	"context"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"

	"github.com/sentinel-official/sentinel-dvpnd/internal/daemon"
)

func TestListen_Busy(t *testing.T) {
	t.Parallel()

	pipe := `\\.\pipe\sentinel-dvpnd-test-` + strconv.Itoa(os.Getpid())

	listener, err := daemon.Listen(t.Context(), pipe, "")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })

	other, err := daemon.Listen(t.Context(), pipe, "")
	require.ErrorContains(t, err, "already listening")
	require.Nil(t, other)

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	accepted := make(chan error, 1)

	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			acceptErr = conn.Close()
		}

		accepted <- acceptErr
	}()

	conn, err := winio.DialPipeAccess(ctx, pipe, 0x120183)
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	select {
	case err := <-accepted:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("accept did not finish before deadline")
	}
}

func TestListen_Canceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	pipe := `\\.\pipe\sentinel-dvpnd-test-canceled-` + strconv.Itoa(os.Getpid())
	listener, err := daemon.Listen(ctx, pipe, "")
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, listener)
}

func TestListen_ConcurrentStart(t *testing.T) {
	t.Parallel()

	pipe := `\\.\pipe\sentinel-dvpnd-test-concurrent-` + strconv.Itoa(os.Getpid())

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	start := make(chan struct{})

	type result struct {
		listener net.Listener
		err      error
	}

	results := make(chan result, 2)

	for range 2 {
		go func() {
			<-start

			listener, err := daemon.Listen(ctx, pipe, "")
			results <- result{listener, err}
		}()
	}

	close(start)

	var successes, busy int

	for range 2 {
		select {
		case result := <-results:
			if result.err == nil {
				successes++

				require.NotNil(t, result.listener)
				t.Cleanup(func() { require.NoError(t, result.listener.Close()) })
			} else {
				busy++

				require.ErrorContains(t, result.err, "already listening")
				require.Nil(t, result.listener)
			}
		case <-ctx.Done():
			t.Fatal("concurrent listeners did not finish before deadline")
		}
	}

	require.Equal(t, 1, successes)
	require.Equal(t, 1, busy)
}

func TestListen_Free(t *testing.T) {
	t.Parallel()

	pipe := `\\.\pipe\sentinel-dvpnd-test-free-` + strconv.Itoa(os.Getpid())

	listener, err := daemon.Listen(t.Context(), pipe, "")
	require.NoError(t, err)
	require.NoError(t, listener.Close())
}

func TestPipeDescriptorForSID(t *testing.T) {
	t.Parallel()

	descriptor := daemon.PipeDescriptorForSID("S-1-5-32-545")
	require.Equal(t, "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;0x120183;;;S-1-5-32-545)", descriptor)
	require.Zero(t, 0x120183&windows.FILE_APPEND_DATA)
}
