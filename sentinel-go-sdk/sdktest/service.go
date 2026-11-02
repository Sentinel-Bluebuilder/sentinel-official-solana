package sdktest

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

const (
	FakeBit  uint32       = 1 << 30
	FakeName service.Name = "fake"
)

const fakeInterface = "fake0"

var (
	_ service.Client = (*FakeClient)(nil)
	_ service.Server = (*FakeServer)(nil)
)

type FakeClient struct {
	state *FakeState
}

func NewFakeClient(state *FakeState) *FakeClient {
	return &FakeClient{state: state}
}

func (c *FakeClient) Interface() string {
	return fakeInterface
}

func (c *FakeClient) Name() service.Name {
	return c.state.name
}

func (c *FakeClient) PeerRequest(_ context.Context) (service.PeerRequest, error) {
	req := service.PeerRequest{
		Name:    c.state.name,
		Payload: []byte(`{"public_key":"client"}`),
	}

	return req, nil
}

func (c *FakeClient) ProxyEndpoint() string {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()

	return c.state.proxyEndpoint
}

func (c *FakeClient) Start(ctx context.Context, host string, _ service.PeerResponse) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("fake client start: %w", err)
	}

	c.state.mu.Lock()
	defer c.state.mu.Unlock()

	c.state.host = host
	c.state.started = true

	return nil
}

func (c *FakeClient) Stats(ctx context.Context) (service.PeerStats, error) {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()

	_, bounded := ctx.Deadline()
	c.state.statsBounded = append(c.state.statsBounded, bounded)

	return c.state.localStats, nil
}

func (c *FakeClient) Stop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("fake client stop: %w", err)
	}

	c.state.mu.Lock()
	defer c.state.mu.Unlock()

	c.state.stopped = true

	return nil
}

func (c *FakeClient) Wait(ctx context.Context) error {
	<-ctx.Done()

	return nil
}

var _ service.Validator = FakeMetadata{}

type FakeMetadata struct {
	Port uint16 `json:"port"`
}

func (FakeMetadata) Validate() error {
	return nil
}

type FakeServer struct {
	state *FakeState
}

func NewFakeServer(state *FakeState) *FakeServer {
	return &FakeServer{state: state}
}

func (s *FakeServer) AddPeer(_ context.Context, req service.PeerRequest) (service.PeerResponse, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	id := service.PeerID(string(rune('a' + s.state.nextPeer)))
	s.state.nextPeer++
	s.state.added = append(s.state.added, req)

	resp := service.PeerResponse{
		ID:      id,
		Name:    s.state.name,
		Payload: []byte(`{"addresses":["10.8.0.2/32"]}`),
	}

	return resp, nil
}

func (s *FakeServer) Metadata() service.ServiceMetadata {
	metadata := service.ServiceMetadata{
		Name:    s.state.name,
		Payload: []byte(`{"port":51820,"public_key":"pub"}`),
	}

	return metadata
}

func (s *FakeServer) Name() service.Name {
	return s.state.name
}

func (s *FakeServer) PeerStats(_ context.Context, id service.PeerID) (service.PeerStats, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	return s.state.peerStats[id], nil
}

func (s *FakeServer) RemovePeer(_ context.Context, id service.PeerID) error {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	s.state.removed = append(s.state.removed, id)

	return nil
}

func (s *FakeServer) Start(_ context.Context) error {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	s.state.started = true

	return nil
}

func (s *FakeServer) Stop(_ context.Context) error {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	s.state.stopped = true

	return nil
}

func (s *FakeServer) Wait(ctx context.Context) error {
	<-ctx.Done()

	return nil
}

type FakeState struct {
	added         []service.PeerRequest
	host          string
	localStats    service.PeerStats
	mu            sync.Mutex
	name          service.Name
	nextPeer      int
	peerStats     map[service.PeerID]service.PeerStats
	proxyEndpoint string
	removed       []service.PeerID
	started       bool
	statsBounded  []bool
	stopped       bool
}

func NewFakeState(name service.Name) *FakeState {
	state := &FakeState{
		name:      name,
		peerStats: map[service.PeerID]service.PeerStats{},
	}

	return state
}

func (s *FakeState) AddedPeers() []service.PeerRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.added)
}

func (s *FakeState) Host() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.host
}

func (s *FakeState) RemovedPeers() []service.PeerID {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.removed)
}

func (s *FakeState) SetLocalStats(stats service.PeerStats) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.localStats = stats
}

func (s *FakeState) SetProxyEndpoint(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.proxyEndpoint = addr
}

func (s *FakeState) SetStats(id service.PeerID, stats service.PeerStats) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.peerStats[id] = stats
}

func (s *FakeState) Started() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.started
}

func (s *FakeState) StatsBounded() []bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.statsBounded)
}

func (s *FakeState) Stopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.stopped
}

func RequireClientRegistered(tb testing.TB, name service.Name, bit uint32, want any) {
	tb.Helper()

	parsed, err := service.ParseName(name.String())
	require.NoError(tb, err)
	require.Equal(tb, name, parsed)
	require.Equal(tb, bit, name.Bit())

	cfg, err := service.DefaultClientConfig(name)
	require.NoError(tb, err)
	require.IsType(tb, want, cfg)

	p := service.Placement{
		Dir:       tb.TempDir(),
		Interface: "dvpn-c0",
		ToolDir:   tb.TempDir(),
	}

	c, err := service.NewClient(name, cfg, p, zerolog.Nop())
	require.NoError(tb, err)
	require.Equal(tb, name, c.Name())
}

func RequireServerRegistered(tb testing.TB, name service.Name, bit uint32, want any) {
	tb.Helper()

	parsed, err := service.ParseName(name.String())
	require.NoError(tb, err)
	require.Equal(tb, name, parsed)
	require.Equal(tb, bit, name.Bit())

	cfg, err := service.DefaultServerConfig(name)
	require.NoError(tb, err)
	require.IsType(tb, want, cfg)

	p := service.Placement{
		Dir:       tb.TempDir(),
		Interface: "dvpn-n0",
		Table:     "sentinel-dvpn-n0",
		ToolDir:   tb.TempDir(),
	}

	s, err := service.NewServer(name, cfg, p, zerolog.Nop())
	require.NoError(tb, err)
	require.Equal(tb, name, s.Name())
}
