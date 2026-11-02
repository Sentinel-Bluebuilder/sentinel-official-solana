package xray_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/xray"
)

func TestNewServer_Registered(t *testing.T) {
	t.Parallel()

	sdktest.RequireServerRegistered(t, xray.Name, 1<<4, xray.ServerConfig{})

	dir, err := service.ServerToolDir(xray.Name)
	require.NoError(t, err)
	require.Equal(t, "/usr/local/etc/xray/sentinel-dvpn", dir)
}
