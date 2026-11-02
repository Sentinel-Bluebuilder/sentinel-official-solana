package cli_test

import (
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
)

func TestPeerConfig_DecodeTOML(t *testing.T) {
	t.Parallel()

	var zero cli.PeerConfig
	require.False(t, zero.AllowPrivate)

	var cfg cli.PeerConfig
	require.NoError(t, toml.Unmarshal([]byte("allow_private = true"), &cfg))
	require.True(t, cfg.AllowPrivate)
}
