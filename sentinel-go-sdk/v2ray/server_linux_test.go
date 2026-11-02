package v2ray_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/v2ray"
)

func TestNewServer_Registered(t *testing.T) {
	t.Parallel()

	sdktest.RequireServerRegistered(t, v2ray.Name, 1<<1, v2ray.ServerConfig{})

	dir, err := service.ServerToolDir(v2ray.Name)
	require.NoError(t, err)
	require.Equal(t, "/usr/local/etc/v2ray/sentinel-dvpn", dir)
}
