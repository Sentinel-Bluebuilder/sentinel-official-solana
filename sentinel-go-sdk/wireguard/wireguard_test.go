package wireguard_test

import (
	"encoding/base64"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
	"github.com/sentinel-official/sentinel-go-sdk/v3/wireguard"
)

func TestNewClient_Registered(t *testing.T) {
	t.Parallel()

	tunnelBinary := "wg-quick"
	if runtime.GOOS == "windows" {
		tunnelBinary = "wireguard"
	}

	require.Equal(t, []string{"wg", tunnelBinary}, service.Binaries(wireguard.Name))

	if runtime.GOOS != "linux" {
		_, err := service.DefaultServerConfig(wireguard.Name)
		require.ErrorIs(t, err, service.ErrUnknownName)
	}

	sdktest.RequireClientRegistered(t, wireguard.Name, 1<<0, wireguard.ClientConfig{})

	dir, err := service.ClientToolDir(wireguard.Name)
	require.NoError(t, err)
	require.Equal(t, "/etc/wireguard/sentinel-dvpn", dir)
}

func TestServerConfig_Validate(t *testing.T) {
	t.Parallel()

	cfg := wireguard.DefaultServerConfig()
	cfg.PrivateKey = "nope"
	err := cfg.Validate()
	require.ErrorIs(t, err, validate.ErrInvalidConfig)
	require.EqualError(t, err, "invalid configuration: private key: key is 3 bytes, want 32")

	cfg = wireguard.DefaultServerConfig()
	cfg.PrivateKey = base64.StdEncoding.EncodeToString([]byte("short"))
	err = cfg.Validate()
	require.ErrorIs(t, err, validate.ErrInvalidConfig)
	require.EqualError(t, err, "invalid configuration: private key: key is 5 bytes, want 32")

	cfg = wireguard.DefaultServerConfig()
	cfg.Addresses = []string{"10.0.0.1"}
	err = cfg.Validate()
	require.ErrorIs(t, err, validate.ErrInvalidConfig)
	require.EqualError(t, err, "invalid configuration: addresses[0] cidr")

	cfg = wireguard.DefaultServerConfig()
	cfg.Addresses = []string{"10.0.0.1/024"}
	err = cfg.Validate()
	require.ErrorIs(t, err, validate.ErrInvalidConfig)
	require.EqualError(t, err, `invalid configuration: address "10.0.0.1/024"`)

	cfg = wireguard.DefaultServerConfig()
	cfg.ListenPort = 80
	err = cfg.Validate()
	require.ErrorIs(t, err, validate.ErrInvalidConfig)
	require.EqualError(t, err, "invalid configuration: listen_port gte=1024")

	cfg = wireguard.DefaultServerConfig()
	cfg.MTU = 1000
	err = cfg.Validate()
	require.ErrorIs(t, err, validate.ErrInvalidConfig)
	require.EqualError(t, err, "invalid configuration: mtu gte=1280")

	cfg = wireguard.DefaultServerConfig()
	cfg.EgressInterface = "0123456789abcdef"
	err = cfg.Validate()
	require.ErrorIs(t, err, validate.ErrInvalidConfig)

	cfg = wireguard.DefaultServerConfig()
	cfg.EgressInterface = ""
	require.NoError(t, cfg.Validate())
}
