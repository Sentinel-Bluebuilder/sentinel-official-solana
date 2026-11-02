package openvpn

import (
	"context"
	"embed"
	"io"
	"net/netip"
	"time"

	"github.com/rs/zerolog"
	"github.com/vishvananda/netlink"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

type ServerTemplateData = serverTemplateData

func ManagementAttach(m *management, w io.Writer) { m.attach(w) }

func ManagementDeliver(m *management, reply string) { m.deliver(reply) }

func NetworkAndNetmask(p netip.Prefix) (string, string) { return networkAndNetmask(p) }

func NewServerForTest(
	cfg ServerConfig,
	p service.Placement,
	log zerolog.Logger,
	mgmt *management,
	setup setupFunc,
	opts ...service.ProcessManagerOption,
) (*Server, error) {
	return newServer(cfg, p, log, mgmt, setup, opts...)
}

func ServerLaunch(s *Server, ctx context.Context, cmd service.Command, probe func(context.Context) error) error {
	return s.process.Launch(ctx, cmd, probe)
}

func ServerWaitPeerGone(s *Server, ctx context.Context, user string) error {
	return s.waitPeerGone(ctx, user)
}

func SetServerLinkHook(s *Server, lookupLink func(string) (netlink.Link, error)) {
	s.lookupLink = lookupLink
}

func SetServerMetadata(s *Server, metadata service.ServiceMetadata) { s.metadata = metadata }

func SetServerRemovePeerTimeout(s *Server, timeout time.Duration) { s.removeTimeout = timeout }

func Templates() embed.FS { return templates }
