package client_test

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/client"
)

func TestRunCommand_FeedsStdin(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("uses a unix command")
	}

	out, err := client.RunCommand(t.Context(), "hello\n", "cat")
	require.NoError(t, err)
	require.Equal(t, "hello\n", string(out))
}

func TestRunCommand_MissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	_, err := client.RunCommand(t.Context(), "", "no-such-tool")
	require.Error(t, err)
}
