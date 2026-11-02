package xray

import (
	"context"

	"github.com/rs/zerolog"

	"github.com/sentinel-official/sentinel-go-sdk/v3/internal/v2raycore"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

//nolint:gochecknoinits
func init() {
	driver := service.ServerDriver[ServerConfig]{
		Binaries: []string{binary},
		Bit:      protocolBit,
		Cleanup:  cleanupServer,
		Defaults: DefaultServerConfig,
		New:      NewServer,
		ToolDir:  toolDir,
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
