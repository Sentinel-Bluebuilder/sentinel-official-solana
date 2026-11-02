package cli_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"

	"github.com/sentinel-official/sentinel-dvpn/internal/cli"
)

func TestPipeClientRights(t *testing.T) {
	t.Parallel()

	require.Equal(t, uint32(0x120183), cli.PipeClientRights)
	require.Zero(t, cli.PipeClientRights&windows.FILE_APPEND_DATA)
}
