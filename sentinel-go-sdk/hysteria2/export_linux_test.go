package hysteria2

import (
	"context"
	"net"
	"net/http"

	"github.com/rs/zerolog"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

type TrafficEntry = trafficEntry

func AuthHandler(peers *service.Peers[struct{}]) http.Handler { return authHandler(peers) }

func FetchTraffic(ctx context.Context, addr, secret string) (map[string]trafficEntry, error) {
	return fetchTraffic(ctx, addr, secret)
}

func Kick(ctx context.Context, addr, secret, id string) error { return kick(ctx, addr, secret, id) }

func NewServerForTest(
	cfg ServerConfig,
	p service.Placement,
	log zerolog.Logger,
	statsAddr string,
	opts ...service.ProcessManagerOption,
) (*Server, error) {
	return newServer(cfg, p, log, statsAddr, opts...)
}

func ServerServeAuthServer(s *Server, srv *http.Server, listener net.Listener) {
	s.serveAuthServer(srv, listener)
}

func ServerTLSPin(s *Server) string { return s.tlsPin }
