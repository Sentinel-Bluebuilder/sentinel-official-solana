package wireguard_test

import (
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
	"github.com/sentinel-official/sentinel-go-sdk/v3/wireguard"
)

func TestNewServer_Registered(t *testing.T) {
	t.Parallel()

	sdktest.RequireServerRegistered(t, wireguard.Name, 1<<0, wireguard.ServerConfig{})

	_, err := wireguard.NewServer(wireguard.DefaultServerConfig(), service.Placement{Dir: t.TempDir()}, zerolog.Nop())
	require.ErrorIs(t, err, validate.ErrInvalidConfig)

	dir, err := service.ServerToolDir(wireguard.Name)
	require.NoError(t, err)
	require.Equal(t, "/etc/wireguard/sentinel-dvpn", dir)
}
