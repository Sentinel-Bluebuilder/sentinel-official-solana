package wireguard

import (
	"context"
	"embed"
	"fmt"
	"runtime"

	"github.com/rs/zerolog"

	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
	"github.com/sentinel-official/sentinel-go-sdk/v3/internal/wgcore"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
)

const Name service.Name = "wireguard"

const (
	binary                     = "wg"
	defaultMTU                 = 1420
	protocolBit         uint32 = 1 << 0
	runDir                     = "/var/run/wireguard"
	toolDir                    = "/etc/wireguard/" + instance.ProductName
	tunnelServicePrefix        = "WireGuardTunnel$"
	userspaceBinary            = "wireguard-go"
)

//nolint:gochecknoglobals
var lookupDirs = func() []string { return nil }

//go:embed templates/*.tmpl
var templates embed.FS

type ClientConfig struct{}

type Extensions struct{}

func (Extensions) Validate() error {
	return nil
}

type ServerConfig struct {
	Addresses       []string `toml:"addresses"        validate:"min=1,dive,cidr"`
	EgressInterface string   `toml:"egress_interface"`
	ListenPort      uint16   `toml:"listen_port"      validate:"gte=1024"`
	MTU             uint16   `toml:"mtu"              validate:"gte=1280"`
	PrivateKey      string   `toml:"private_key"      validate:"required,base64"`
}

func (c ServerConfig) DerivePublicKey() (string, error) {
	pub, err := wgcore.DerivePublicKey(c.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("%w: private key: %w", validate.ErrInvalidConfig, err)
	}

	return pub, nil
}

func (c ServerConfig) Validate() error {
	if err := validate.Struct(c); err != nil {
		return err
	}

	if c.EgressInterface != "" {
		if err := service.ValidateInterfaceName(c.EgressInterface); err != nil {
			return err
		}
	}

	if _, err := service.ParsePrefixes(c.Addresses); err != nil {
		return err
	}

	_, err := c.DerivePublicKey()

	return err
}

//nolint:gochecknoinits
func init() {
	driver := service.ClientDriver[ClientConfig]{
		Binaries:   []string{binary, tunnelBinary()},
		Bit:        protocolBit,
		Defaults:   DefaultClientConfig,
		LookupDirs: func() []string { return lookupDirs() },
		Metadata:   func() service.Validator { return &wgcore.Metadata[Extensions]{} },
		New:        NewClient,
		Preflight: func(_ context.Context, _ ClientConfig) []service.PreflightCheck {
			return wgcore.PreflightUserspace(variant(), runtime.GOOS)
		},
		ToolDir: toolDir,
	}
	service.RegisterClient(Name, driver)
}

func DefaultClientConfig() ClientConfig {
	return ClientConfig{}
}

func DefaultServerConfig() ServerConfig {
	key, err := wgcore.NewPrivateKey()
	if err != nil {
		panic(err)
	}

	return ServerConfig{
		Addresses:  []string{service.RandomPrefix().String()},
		ListenPort: service.RandomPort(),
		MTU:        defaultMTU,
		PrivateKey: key,
	}
}

func NewClient(_ ClientConfig, p service.Placement, log zerolog.Logger) (service.Client, error) {
	return wgcore.NewClient[Extensions](variant(), p, log)
}

func tunnelBinary() string {
	if runtime.GOOS == "windows" {
		return "wireguard"
	}

	return "wg-quick"
}

func variant() wgcore.Variant {
	return wgcore.Variant{
		Binary:              binary,
		Confinable:          true,
		Name:                Name,
		RunDir:              runDir,
		Templates:           templates,
		TunnelBinary:        tunnelBinary(),
		TunnelServicePrefix: tunnelServicePrefix,
		UserspaceBinary:     userspaceBinary,
	}
}
