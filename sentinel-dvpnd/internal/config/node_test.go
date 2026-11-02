package config_test

import (
	"testing"

	"github.com/creasty/defaults"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
)

func TestDefaultNode(t *testing.T) {
	t.Parallel()

	var want config.Node
	require.NoError(t, defaults.Set(&want))
	require.Equal(t, want, config.DefaultNode())
	require.EqualValues(t, 5, want.Sessions.HeartbeatSeconds)
	require.EqualValues(t, 100_000_000, want.Sessions.MinPrepayBytes)
	require.EqualValues(t, 60, want.Sessions.MinPrepaySeconds)
	require.EqualValues(t, 3600, want.Lease.MinPrepaySeconds)
}
