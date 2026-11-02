package v2raycore

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"google.golang.org/grpc"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

const (
	clientTemplate      = "templates/client.json.tmpl"
	configFlag          = "-config"
	outboundStatsPrefix = "outbound>>>out>>>"
)

var _ service.ClientCleaner = (*Client[Extensions])(nil)

type Client[E Extensions] struct {
	cfg       ClientConfig[E]
	conf      string
	conn      grpc.ClientConnInterface
	log       zerolog.Logger
	placement service.Placement
	process   *service.ProcessManager
	uuid      string
	variant   Variant
}

func NewClient[E Extensions](
	v Variant,
	cfg ClientConfig[E],
	p service.Placement,
	log zerolog.Logger,
) (service.Client, error) {
	return newClient(v, cfg, p, log), nil
}

func newClient[E Extensions](
	v Variant,
	cfg ClientConfig[E],
	p service.Placement,
	log zerolog.Logger,
	opts ...service.ProcessManagerOption,
) *Client[E] {
	log = service.ScopeLogger(log, v.Name)

	return &Client[E]{
		cfg:       cfg,
		conf:      p.ToolPath(".json"),
		log:       log,
		placement: p,
		process:   service.NewProcessManager(v.Binary, p.Dir, log, opts...),
		variant:   v,
	}
}

func (c *Client[E]) Cleanup(ctx context.Context) error {
	if err := c.process.KillStale(ctx, configFlag, c.conf); err != nil {
		return err
	}

	return service.RemoveOwned(c.placement.ToolDir, c.placement.Interface)
}

func (c *Client[E]) Interface() string {
	return ""
}

func (c *Client[E]) Name() service.Name {
	return c.variant.Name
}

func (c *Client[E]) PeerRequest(_ context.Context) (service.PeerRequest, error) {
	req := PeerRequest{UUID: uuid.NewString()}

	packed, err := service.PackRequest(c.variant.Name, req)
	if err != nil {
		return service.PeerRequest{}, err
	}

	c.uuid = req.UUID

	return packed, nil
}

func (c *Client[E]) ProxyEndpoint() string {
	return service.LoopbackAddr(c.cfg.ProxyPort)
}

func (c *Client[E]) Start(ctx context.Context, host string, resp service.PeerResponse) error {
	if resp.Name != c.variant.Name {
		return fmt.Errorf("%w: service %q", service.ErrInvalidPeer, resp.Name)
	}

	p, err := service.UnpackResponse[PeerResponse[E]](resp)
	if err != nil {
		return err
	}

	if string(resp.ID) != c.uuid || p.UUID != c.uuid {
		return fmt.Errorf("%w: uuid does not match the peer request", service.ErrInvalidPeer)
	}

	if err := service.CheckPortFree(ctx, c.cfg.APIPort); err != nil {
		return fmt.Errorf("starting %s: %w", c.variant.Binary, err)
	}

	if c.variant.CheckVersion != nil {
		if err := c.variant.CheckVersion(ctx); err != nil {
			return fmt.Errorf("starting %s: %w", c.variant.Binary, err)
		}
	}

	conf, err := c.writeConf(host, p)
	if err != nil {
		return err
	}

	succeeded := false

	defer func() {
		if !succeeded {
			_ = service.RemoveOwned(c.placement.ToolDir, c.placement.Interface)
		}
	}()

	cmd := service.Command{
		Args:  []string{"run", configFlag, conf},
		Path:  c.variant.Binary,
		Unset: confDirEnvs,
	}

	probe := func(ctx context.Context) error {
		conn, err := dialAPI(ctx, c.variant, c.cfg.APIPort)
		if err != nil {
			return err
		}

		c.conn = conn

		return nil
	}

	if err := c.process.Launch(ctx, cmd, probe); err != nil {
		return err
	}

	succeeded = true

	c.log.Info().Str("process", c.variant.Binary).Msg("service started")

	return nil
}

func (c *Client[E]) Stats(ctx context.Context) (service.PeerStats, error) {
	if c.conn == nil {
		return service.PeerStats{}, fmt.Errorf("querying stats: %w", service.ErrServiceNotStarted)
	}

	stats, err := c.variant.API.QueryStats(ctx, c.conn, outboundStatsPrefix)
	if err != nil {
		return service.PeerStats{}, fmt.Errorf("querying stats: %w", err)
	}

	return service.PeerStats{
		RxBytes: uint64(stats[outboundStatsPrefix+"traffic>>>downlink"]),
		TxBytes: uint64(stats[outboundStatsPrefix+"traffic>>>uplink"]),
	}, nil
}

func (c *Client[E]) Stop(ctx context.Context) error {
	return c.process.Do(func() error {
		if !c.process.Started() {
			return nil
		}

		if conn, ok := c.conn.(*grpc.ClientConn); ok {
			_ = conn.Close()
		}

		if err := c.process.Terminate(ctx); err != nil {
			return err
		}

		if err := service.RemoveOwned(c.placement.ToolDir, c.placement.Interface); err != nil {
			return err
		}

		c.log.Info().Str("process", c.variant.Binary).Msg("service stopped")

		return nil
	})
}

func (c *Client[E]) Wait(ctx context.Context) error {
	return c.process.Wait(ctx)
}

func (c *Client[E]) writeConf(host string, resp PeerResponse[E]) (string, error) {
	data := clientTemplateData[E]{
		APIPort:   c.cfg.APIPort,
		Host:      host,
		Outbound:  selectOutbound(c.variant.SecurityPreferences, resp.Inbounds),
		ProxyPort: c.cfg.ProxyPort,
		UUID:      resp.UUID,
	}

	if err := service.WriteTemplate(c.variant.Templates, clientTemplate, c.conf, data, "//"); err != nil {
		return "", err
	}

	return c.conf, nil
}

type clientTemplateData[E Extensions] struct {
	APIPort   uint16
	Host      string
	Outbound  Inbound[E]
	ProxyPort uint16
	UUID      string
}

func selectOutbound[E Extensions](preferences []string, inbounds []Inbound[E]) Inbound[E] {
	for _, security := range preferences {
		for _, in := range inbounds {
			if in.Security == security {
				return in
			}
		}
	}

	return inbounds[0]
}
