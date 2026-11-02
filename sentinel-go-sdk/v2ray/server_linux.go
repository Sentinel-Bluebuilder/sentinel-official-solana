package v2ray

import (
	"context"

	"github.com/rs/zerolog"

	"github.com/sentinel-official/sentinel-go-sdk/v3/internal/v2raycore"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

func (i Inbound) toCore() v2raycore.Inbound[Extensions] {
	return v2raycore.Inbound[Extensions]{
		Port:      i.Port,
		Protocol:  i.Protocol,
		Security:  i.Security,
		Transport: i.Transport,
	}
}

func (c ServerConfig) toCore() v2raycore.ServerConfig[Extensions] {
	inbounds := make([]v2raycore.Inbound[Extensions], 0, len(c.Inbounds))
	for _, in := range c.Inbounds {
		inbounds = append(inbounds, in.toCore())
	}

	return v2raycore.ServerConfig[Extensions]{
		APIPort:  c.APIPort,
		Inbounds: inbounds,
	}
}

//nolint:gochecknoinits
func init() {
	driver := service.ServerDriver[ServerConfig]{
		Binaries: []string{binary},
		Bit:      protocolBit,
		Cleanup:  cleanupServer,
		Defaults: DefaultServerConfig,
		New:      NewServer,
		Preflight: func(ctx context.Context, _ ServerConfig) []service.PreflightCheck {
			return preflightVersion(ctx, service.SeverityError)
		},
		ToolDir: toolDir,
	}
	service.RegisterServer(Name, driver)
}

func NewServer(cfg ServerConfig, p service.Placement, log zerolog.Logger) (service.Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return v2raycore.NewServer(variant(), cfg.toCore(), p, log)
}

func cleanupServer(ctx context.Context, p service.Placement) error {
	return v2raycore.CleanupServer(ctx, variant(), p)
}
