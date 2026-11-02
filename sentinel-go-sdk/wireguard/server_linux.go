package wireguard

import (
	"context"
	"runtime"

	"github.com/rs/zerolog"

	"github.com/sentinel-official/sentinel-go-sdk/v3/internal/wgcore"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

func (c ServerConfig) toCore() (wgcore.ServerConfig[Extensions], error) {
	prefixes, err := service.ParsePrefixes(c.Addresses)
	if err != nil {
		return wgcore.ServerConfig[Extensions]{}, err
	}

	return wgcore.ServerConfig[Extensions]{
		Addresses:       prefixes,
		EgressInterface: c.EgressInterface,
		ListenPort:      c.ListenPort,
		MTU:             c.MTU,
		PrivateKey:      c.PrivateKey,
	}, nil
}

//nolint:gochecknoinits
func init() {
	driver := service.ServerDriver[ServerConfig]{
		Binaries: []string{binary, tunnelBinary()},
		Bit:      protocolBit,
		Defaults: DefaultServerConfig,
		New:      NewServer,
		Preflight: func(_ context.Context, _ ServerConfig) []service.PreflightCheck {
			return wgcore.PreflightUserspace(variant(), runtime.GOOS)
		},
		ToolDir: toolDir,
	}
	service.RegisterServer(Name, driver)
}

func NewServer(cfg ServerConfig, p service.Placement, log zerolog.Logger) (service.Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	c, err := cfg.toCore()
	if err != nil {
		return nil, err
	}

	return wgcore.NewServer(variant(), c, p, log)
}
