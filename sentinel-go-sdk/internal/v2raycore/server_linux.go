package v2raycore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

const (
	serverTemplate  = "templates/server.json.tmpl"
	userStatsPrefix = "user>>>"
)

type Server[E Extensions] struct {
	certFile  string
	cfg       ServerConfig[E]
	conf      string
	conn      grpc.ClientConnInterface
	keyFile   string
	log       zerolog.Logger
	metadata  service.ServiceMetadata
	mu        sync.Mutex
	peers     *service.Peers[[]string]
	placement service.Placement
	process   *service.ProcessManager
	tlsPin    string
	variant   Variant
}

func NewServer[E Extensions](
	v Variant,
	cfg ServerConfig[E],
	p service.Placement,
	log zerolog.Logger,
) (service.Server, error) {
	return newServer(v, cfg, p, log), nil
}

func newServer[E Extensions](
	v Variant,
	cfg ServerConfig[E],
	p service.Placement,
	log zerolog.Logger,
	opts ...service.ProcessManagerOption,
) *Server[E] {
	log = service.ScopeLogger(log, v.Name)

	return &Server[E]{
		certFile:  p.ToolPath(".tls.crt"),
		cfg:       cfg,
		conf:      serverConf(p),
		keyFile:   p.ToolPath(".tls.key"),
		log:       log,
		peers:     service.NewPeers[[]string](),
		placement: p,
		process:   service.NewProcessManager(v.Binary, p.Dir, log, opts...),
		variant:   v,
	}
}

func (s *Server[E]) AddPeer(ctx context.Context, req service.PeerRequest) (service.PeerResponse, error) {
	p, err := service.UnpackRequest[PeerRequest](req)
	if err != nil {
		return service.PeerResponse{}, err
	}

	id := service.PeerID(p.UUID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.process.Started() {
		return service.PeerResponse{}, fmt.Errorf("adding peer %s: %w", id, service.ErrServiceNotStarted)
	}

	if s.peers.Has(id) {
		return service.PeerResponse{}, fmt.Errorf("%w: %s", service.ErrPeerExists, id)
	}

	added := make([]string, 0, len(s.cfg.Inbounds))
	for _, in := range s.cfg.Inbounds {
		if err := s.variant.API.AddUser(ctx, s.conn, in.Tag(), in.Protocol, p.UUID); err != nil {
			for _, tag := range added {
				_ = s.variant.API.RemoveUser(ctx, s.conn, tag, p.UUID)
			}

			return service.PeerResponse{}, fmt.Errorf("adding peer %s: %w", id, err)
		}

		added = append(added, in.Tag())
	}

	resp := PeerResponse[E]{
		Inbounds: s.publicInbounds(),
		UUID:     p.UUID,
	}

	packed, err := service.PackResponse(s.variant.Name, id, resp)
	if err != nil {
		return service.PeerResponse{}, errors.Join(err, s.removeUsers(ctx, added, p.UUID))
	}

	if err := s.peers.Add(id, added); err != nil {
		return service.PeerResponse{}, errors.Join(err, s.removeUsers(ctx, added, p.UUID))
	}

	s.log.Debug().Str("peer", string(id)).Msg("peer added")

	return packed, nil
}

func (s *Server[E]) Metadata() service.ServiceMetadata {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.metadata
}

func (s *Server[E]) Name() service.Name {
	return s.variant.Name
}

func (s *Server[E]) PeerStats(ctx context.Context, id service.PeerID) (service.PeerStats, error) {
	s.mu.Lock()
	started, known := s.process.Started(), s.peers.Has(id)
	s.mu.Unlock()

	if !started {
		return service.PeerStats{}, fmt.Errorf("querying stats for %s: %w", id, service.ErrServiceNotStarted)
	}

	if !known {
		return service.PeerStats{}, fmt.Errorf("%w: %s", service.ErrPeerNotFound, id)
	}

	stats, err := s.variant.API.QueryStats(ctx, s.conn, userStatsPrefix+string(id)+">>>")
	if err != nil {
		return service.PeerStats{}, fmt.Errorf("querying stats: %w", err)
	}

	return service.PeerStats{
		RxBytes: uint64(stats[userStatsPrefix+string(id)+">>>traffic>>>uplink"]),
		TxBytes: uint64(stats[userStatsPrefix+string(id)+">>>traffic>>>downlink"]),
	}, nil
}

func (s *Server[E]) RemovePeer(ctx context.Context, id service.PeerID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.process.Started() {
		return fmt.Errorf("removing peer %s: %w", id, service.ErrServiceNotStarted)
	}

	tags, err := s.peers.Get(id)
	if err != nil {
		return err
	}

	var (
		errs      []error
		remaining []string
	)

	for _, tag := range tags {
		if err := s.variant.API.RemoveUser(ctx, s.conn, tag, string(id)); err != nil {
			errs = append(errs, err)
			remaining = append(remaining, tag)
		}
	}

	if len(remaining) > 0 {
		setErr := s.peers.Set(id, remaining)

		return fmt.Errorf("removing peer %s: %w", id, errors.Join(append(errs, setErr)...))
	}

	if err := s.peers.Remove(id); err != nil {
		return err
	}

	s.log.Debug().Str("peer", string(id)).Msg("peer removed")

	return nil
}

func (s *Server[E]) Start(ctx context.Context) error {
	if err := s.process.KillStale(ctx, configFlag, s.conf); err != nil {
		return fmt.Errorf("stopping previous %s: %w", s.variant.Binary, err)
	}

	if err := s.loadCredentials(); err != nil {
		return err
	}

	succeeded := false

	defer func() {
		if !succeeded {
			_ = service.RemoveOwned(s.placement.ToolDir, s.placement.Interface)
		}
	}()

	if err := s.writeConf(); err != nil {
		return err
	}

	m := Metadata[E]{Inbounds: s.publicInbounds()}

	md, err := service.PackMetadata(s.variant.Name, m)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.metadata = md
	s.mu.Unlock()

	cmd := service.Command{
		Args:  []string{"run", configFlag, s.conf},
		Path:  s.variant.Binary,
		Unset: confDirEnvs,
	}

	probe := func(ctx context.Context) error {
		conn, err := dialAPI(ctx, s.variant, s.cfg.APIPort)
		if err != nil {
			return err
		}

		s.conn = conn

		return nil
	}

	if err := s.process.Launch(ctx, cmd, probe); err != nil {
		return err
	}

	succeeded = true

	s.log.Info().Str("process", s.variant.Binary).Msg("service started")

	return nil
}

func (s *Server[E]) Stop(ctx context.Context) error {
	return s.process.Do(func() error {
		if !s.process.Started() {
			return nil
		}

		if conn, ok := s.conn.(*grpc.ClientConn); ok {
			_ = conn.Close()
		}

		if err := s.process.Terminate(ctx); err != nil {
			return err
		}

		if err := service.RemoveOwned(s.placement.ToolDir, s.placement.Interface); err != nil {
			return err
		}

		s.log.Info().Str("process", s.variant.Binary).Msg("service stopped")

		return nil
	})
}

func (s *Server[E]) Wait(ctx context.Context) error {
	return s.process.Wait(ctx)
}

func (s *Server[E]) loadCredentials() error {
	_, tlsPin, err := api.LoadOrCreateServerCert(s.placement.Dir)
	if err != nil {
		return fmt.Errorf("loading server certificate: %w", err)
	}

	s.tlsPin = tlsPin

	return nil
}

func (s *Server[E]) publicInbounds() []Inbound[E] {
	out := make([]Inbound[E], 0, len(s.cfg.Inbounds))
	for _, in := range s.cfg.Inbounds {
		if in.Security == securityTLS {
			in.TLSPin = s.tlsPin
		}

		out = append(out, in)
	}

	return out
}

func (s *Server[E]) removeUsers(ctx context.Context, tags []string, id string) error {
	var errs []error

	for _, tag := range tags {
		if err := s.variant.API.RemoveUser(ctx, s.conn, tag, id); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (s *Server[E]) writeConf() error {
	data := serverTemplateData[E]{
		APIPort:  s.cfg.APIPort,
		CertFile: s.certFile,
		Inbounds: s.cfg.Inbounds,
		KeyFile:  s.keyFile,
	}

	if err := service.CopyOwned(filepath.Join(s.placement.Dir, "tls.crt"), s.certFile, "#"); err != nil {
		return err
	}

	if err := service.CopyOwned(filepath.Join(s.placement.Dir, "tls.key"), s.keyFile, "#"); err != nil {
		return err
	}

	return service.WriteTemplate(s.variant.Templates, serverTemplate, s.conf, data, "//")
}

type serverTemplateData[E Extensions] struct {
	APIPort  uint16
	CertFile string
	Inbounds []Inbound[E]
	KeyFile  string
}

func CleanupServer(ctx context.Context, v Variant, p service.Placement) error {
	return service.KillPIDFile(ctx, service.PIDPath(p.Dir, v.Binary), v.Binary, configFlag, serverConf(p))
}

func serverConf(p service.Placement) string {
	return p.ToolPath(".json")
}
