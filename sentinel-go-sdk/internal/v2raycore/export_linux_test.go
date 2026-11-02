package v2raycore

import (
	"github.com/rs/zerolog"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

func NewServerForTest[E Extensions](
	v Variant,
	cfg ServerConfig[E],
	p service.Placement,
	log zerolog.Logger,
	opts ...service.ProcessManagerOption,
) *Server[E] {
	return newServer(v, cfg, p, log, opts...)
}

func ServerTLSPin[E Extensions](s *Server[E]) string { return s.tlsPin }
