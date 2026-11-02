package v2raycore

import (
	"github.com/rs/zerolog"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

const SecurityTLS = securityTLS

func ClientWriteConf[E Extensions](c *Client[E], host string, resp PeerResponse[E]) (string, error) {
	return c.writeConf(host, resp)
}

func NewClientForTest[E Extensions](
	v Variant,
	cfg ClientConfig[E],
	p service.Placement,
	log zerolog.Logger,
	opts ...service.ProcessManagerOption,
) *Client[E] {
	return newClient(v, cfg, p, log, opts...)
}
