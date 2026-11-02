package amneziawg_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/amneziawg"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

func TestNewServer_Registered(t *testing.T) {
	t.Parallel()

	sdktest.RequireServerRegistered(t, amneziawg.Name, 1<<3, amneziawg.ServerConfig{})

	dir, err := service.ServerToolDir(amneziawg.Name)
	require.NoError(t, err)
	require.Equal(t, "/etc/amnezia/amneziawg/sentinel-dvpn", dir)
}
