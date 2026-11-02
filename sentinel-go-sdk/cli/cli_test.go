package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/version"
)

//nolint:paralleltest
func TestNewVersionCmd(t *testing.T) {
	version.Tag = "v0.0.0"
	version.Commit = "00000000"

	t.Cleanup(func() {
		version.Tag = "unknown"
		version.Commit = "unknown"
	})

	cmd := cli.NewVersionCmd()

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)

	require.NoError(t, cmd.Execute())

	var got version.Info
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Equal(t, "v0.0.0", got.Tag)
	require.Equal(t, "00000000", got.Commit)
}

//nolint:paralleltest
func TestRun_SignalExitCodes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process signals are unavailable on Windows")
	}

	canceled := status.Error(codes.Canceled, "context canceled")

	for _, tc := range []struct {
		err    error
		name   string
		sig    os.Signal
		stderr string
		want   int
	}{
		{
			err:  canceled,
			name: "interrupt",
			sig:  syscall.SIGINT,
			want: 130,
		},
		{
			err:  canceled,
			name: "terminate",
			sig:  syscall.SIGTERM,
			want: 143,
		},
		{
			err:    errors.Join(context.Canceled, errors.New("removing dvpn-n0: boom")),
			name:   "terminate with stop error",
			sig:    syscall.SIGTERM,
			stderr: "removing dvpn-n0: boom",
			want:   143,
		},
		{
			err:    fmt.Errorf("removing dvpn-n0: %w", context.Canceled),
			name:   "terminate with canceled stop step",
			sig:    syscall.SIGTERM,
			stderr: "removing dvpn-n0: context canceled",
			want:   143,
		},
		{
			err:  fmt.Errorf("calling daemon: %w", canceled),
			name: "interrupt with wrapped call",
			sig:  syscall.SIGINT,
			want: 130,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := &cobra.Command{
				Use: "root",
				RunE: func(cmd *cobra.Command, _ []string) error {
					process, err := os.FindProcess(os.Getpid())
					require.NoError(t, err)
					require.NoError(t, process.Signal(tc.sig))
					<-cmd.Context().Done()

					return tc.err
				},
			}

			var stderr bytes.Buffer

			root.SetArgs(nil)
			root.SetErr(&stderr)

			require.Equal(t, tc.want, cli.Run(root))

			if tc.stderr == "" {
				require.Empty(t, stderr.String())
			} else {
				require.Contains(t, stderr.String(), tc.stderr)
			}
		})
	}
}

func TestRun_Success(t *testing.T) {
	t.Parallel()

	noop := func(_ *cobra.Command, _ []string) error {
		return nil
	}
	root := &cobra.Command{
		RunE: noop,
		Use:  "root",
	}
	root.SetArgs(nil)

	require.Equal(t, 0, cli.Run(root))
}
