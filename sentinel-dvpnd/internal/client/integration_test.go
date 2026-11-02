//go:build integration && linux

package client_test

import (
	"context"
	"crypto/rand"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"gorm.io/gorm"

	"github.com/sentinel-official/sentinel-dvpnd/internal/client"
	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-dvpnd/internal/daemon"
	"github.com/sentinel-official/sentinel-dvpnd/internal/rpcpin"
	"github.com/sentinel-official/sentinel-dvpnd/internal/store"
	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	"github.com/sentinel-official/sentinel-dvpnd/internal/worker"
	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
	"github.com/sentinel-official/sentinel-go-sdk/v3/wireguard"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const (
	clientBondTokens     = 5_000_000_000
	clientFundTokens     = 20_000_000_000
	clientGrowthBytes    = 600_000_000
	clientNodePricePerGB = 5_000
	clientSOL            = 5
)

type integrationFixture struct {
	conn          *grpc.ClientConn
	db            *gorm.DB
	deviceKey     solana.PrivateKey
	dnsBackend    *recordingDNSBackend
	firewall      *recordingFirewallBackend
	nodeAddress   solana.PublicKey
	nodeOwnerKey  solana.PrivateKey
	operatorKey   solana.PrivateKey
	port          uint16
	programClient *program.Client
	rt            *client.Runtime
	state         *sdktest.FakeState
}

func newIntegrationFixture(t *testing.T, localnet *sdktest.Localnet) *integrationFixture {
	t.Helper()
	ctx := t.Context()
	programClient := program.NewClient(localnet.RPC, localnet.ProgramID, localnet.Mint, rpc.CommitmentConfirmed)
	device, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	require.NoError(t, localnet.FundSOL(ctx, device.PublicKey(), clientSOL))
	require.NoError(t, localnet.FundTokens(ctx, device.PublicKey(), clientFundTokens))

	nodeOwner, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	require.NoError(t, localnet.FundSOL(ctx, nodeOwner.PublicKey(), clientSOL))
	require.NoError(t, localnet.FundTokens(ctx, nodeOwner.PublicKey(), clientFundTokens))

	operator, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	require.NoError(t, localnet.FundSOL(ctx, operator.PublicKey(), clientSOL))

	port, err := service.FreePort()
	require.NoError(t, err)
	nodeAddress := openNodeOnChain(t, localnet, programClient, nodeOwner, operator, port)
	dir := t.TempDir()
	cfg := config.DefaultClient()
	cfg.EscrowBaseUnits = clientBondTokens
	cfg.NodeTimeoutSeconds = 1
	cfg.Owner = device.PublicKey().String()
	rpcCfg := cli.DefaultRPCConfig(cli.NetworkLocalnet)
	rpcCfg.URL = localnet.RPCURL
	rpcCfg.WSURL = localnet.WSURL
	state := sdktest.NewFakeState(sdktest.FakeName)

	registerFakeClientOnce()

	loaded := &config.Loaded{
		Client:   &cfg,
		Dir:      dir,
		File:     config.File{Peer: cli.PeerConfig{AllowPrivate: true}, RPC: rpcCfg},
		Role:     config.RoleClient,
		Root:     t.TempDir(),
		Services: map[service.Name]any{sdktest.FakeName: fakeClientConfig{state: state}},
		Tag:      testutil.NewInstanceTag(t, config.RoleClient),
	}
	db, err := store.Open(filepath.Join(loaded.Dir, "data.db"))
	require.NoError(t, err)
	pin, err := rpcpin.New(rpcCfg.URL, rpcCfg.Dialer())
	require.NoError(t, err)

	reg := daemon.NewRegistry()
	roleReg := prometheus.WrapRegistererWith(prometheus.Labels{"role": config.RoleClient}, reg)
	log := zerolog.Nop()
	sender := tx.NewSender(localnet.RPC, nil, tx.DefaultConfig(), log)
	rt, err := client.New(loaded, db, device, programClient, sender, log, roleReg, pin)
	require.NoError(t, err)
	require.NoError(t, store.Migrate(ctx, db, rt.Models()...))

	firewall := &recordingFirewallBackend{}
	dns := &recordingDNSBackend{}

	client.SetFirewallBackend(rt, firewall)
	client.SetDNSBackend(rt, dns)
	client.SetInterfaceRemover(rt, &testutil.RecordingInterfaceRemover{})
	client.SetTiming(rt, 30*time.Millisecond, 30*time.Millisecond, 8, 30*time.Millisecond)
	require.NoError(t, rt.Recover(ctx))
	conn := startDaemon(t, rt, dir, reg, roleReg)

	return &integrationFixture{
		conn:          conn,
		db:            db,
		deviceKey:     device,
		dnsBackend:    dns,
		firewall:      firewall,
		nodeAddress:   nodeAddress,
		nodeOwnerKey:  nodeOwner,
		operatorKey:   operator,
		port:          port,
		programClient: programClient,
		rt:            rt,
		state:         state,
	}
}

//nolint:paralleltest
func TestRuntime_Localnet(t *testing.T) {
	localnet := sdktest.StartLocalnet(t)
	fixture := newIntegrationFixture(t, localnet)
	nodeServer, nodeService := testutil.StartFakeNodeServer(t, newFakeNodeConfig(fixture))

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	startFirstSession(t, ctx, fixture, nodeService)
	checkReconnect(t, ctx, fixture, nodeService, nodeServer)
}

func associatedTokenAddress(t *testing.T, programClient *program.Client, owner solana.PublicKey) solana.PublicKey {
	t.Helper()

	addr, err := programClient.AssociatedTokenAddress(owner)
	require.NoError(t, err)

	return addr
}

func checkReconnect(
	t *testing.T,
	ctx context.Context,
	fixture *integrationFixture,
	firstService *testutil.FakeNodeService,
	firstServer *httptest.Server,
) {
	t.Helper()

	control := daemonv1.NewClientServiceClient(fixture.conn)
	fixture.state.SetLocalStats(service.PeerStats{})
	firstService.SetUsage(session.Usage{})

	started, err := control.StartSession(ctx, newChannelRequest(fixture.nodeAddress.String()))
	require.NoError(t, err)

	sess := started.GetSession()
	require.Equal(t, client.SessionStatusActive, sess.GetStatus())

	channels, err := control.ListChannels(ctx, &daemonv1.ListChannelsRequest{})
	require.NoError(t, err)
	require.Len(t, channels.GetChannels(), 2)
	before := len(channels.GetChannels())

	firstServer.Close()
	require.Eventually(t, func() bool {
		response, err := control.GetSession(ctx, &daemonv1.GetSessionRequest{Id: sess.GetId()})

		return err == nil && response.GetSession().GetStatus() == client.SessionStatusEnded &&
			response.GetSession().GetError() == "connection lost"
	}, 10*time.Second, 20*time.Millisecond)

	//nolint:contextcheck // the fake node server lives until the test ends
	_, secondService := testutil.StartFakeNodeServer(t, newFakeNodeConfig(fixture))

	require.Eventually(t, func() bool {
		response, err := control.ListSessions(ctx, &daemonv1.ListSessionsRequest{})
		if err != nil {
			return false
		}

		for _, current := range response.GetSessions() {
			if current.GetStatus() == client.SessionStatusActive && current.GetId() != sess.GetId() &&
				current.GetChannel() == sess.GetChannel() {
				return true
			}
		}

		return false
	}, 20*time.Second, 50*time.Millisecond)

	channels, err = control.ListChannels(ctx, &daemonv1.ListChannelsRequest{})
	require.NoError(t, err)
	require.Len(t, channels.GetChannels(), before)
	require.Eventually(t, func() bool { return secondService.OpenedCount() >= 1 }, 10*time.Second, 50*time.Millisecond)

	_, err = control.StopSession(ctx, &daemonv1.StopSessionRequest{})
	require.NoError(t, err)
}

func newFakeNodeConfig(fixture *integrationFixture) testutil.FakeNodeServerConfig {
	return testutil.FakeNodeServerConfig{
		Name:          sdktest.FakeName,
		Node:          fixture.nodeAddress,
		Owner:         fixture.nodeOwnerKey.PublicKey(),
		Port:          fixture.port,
		ProgramClient: fixture.programClient,
		ProgramID:     fixture.programClient.ProgramID,
		Settlement: nodeapi.Settlement{
			HeartbeatSeconds:        1,
			MinChannelPrepayBytes:   1,
			MinChannelPrepaySeconds: 1,
			MinLeasePrepaySeconds:   1,
			Prices:                  nodeapi.Prices{ChannelPricePerGB: clientNodePricePerGB},
		},
		Signer: fixture.operatorKey,
	}
}

func nodeURIAt(port uint16) string {
	return "https://127.0.0.1:" + strconv.Itoa(int(port))
}

func openNodeOnChain(
	t *testing.T,
	localnet *sdktest.Localnet,
	programClient *program.Client,
	nodeOwner,
	operator solana.PrivateKey,
	port uint16,
) solana.PublicKey {
	t.Helper()
	ctx := t.Context()
	vault, err := programClient.VaultAddress()
	require.NoError(t, err)

	var nodeID [32]uint8

	_, err = rand.Read(nodeID[:])
	require.NoError(t, err)

	prices := dvpn.NodePrices{
		ChannelPricePerGB: clientNodePricePerGB,
	}
	openNodeParams := dvpn.OpenNodeParams{
		Bond:              clientBondTokens,
		Country:           [2]uint8{'D', 'E'},
		Mint:              localnet.Mint,
		NodeID:            nodeID,
		NodeRentRecipient: nodeOwner.PublicKey(),
		Owner:             nodeOwner.PublicKey(),
		OwnerToken:        associatedTokenAddress(t, programClient, nodeOwner.PublicKey()),
		Prices:            prices,
		Protocols:         wireguard.Name.Bit(),
		RentPayer:         nodeOwner.PublicKey(),
		Signer:            nodeOwner.PublicKey(),
		URI:               []byte(nodeURIAt(port)),
		Vault:             vault,
	}
	openNodeIx, err := dvpn.BuildOpenNodeInstruction(localnet.ProgramID, openNodeParams)
	require.NoError(t, err)

	openNodeIxs := []solana.Instruction{openNodeIx}
	_, err = tx.Send(ctx, localnet.RPC, localnet.WS, nodeOwner, tx.DefaultConfig(), openNodeIxs)
	require.NoError(t, err)

	openGrantParams := dvpn.OpenGrantParams{
		Allowance:          dvpn.AllowanceUnlimited,
		ExpiresAt:          0,
		Grantee:            operator.PublicKey(),
		Granter:            nodeOwner.PublicKey(),
		GrantRentRecipient: nodeOwner.PublicKey(),
		Permissions:        program.PermsNode,
		RentPayer:          nodeOwner.PublicKey(),
		Target:             solana.PublicKey{},
	}
	openGrantIx, err := dvpn.BuildOpenGrantInstruction(localnet.ProgramID, openGrantParams)
	require.NoError(t, err)

	openGrantIxs := []solana.Instruction{openGrantIx}
	_, err = tx.Send(ctx, localnet.RPC, localnet.WS, nodeOwner, tx.DefaultConfig(), openGrantIxs)
	require.NoError(t, err)

	nodeAddress, _, err := dvpn.FindNodeAddress(localnet.ProgramID, nodeOwner.PublicKey(), nodeID)
	require.NoError(t, err)

	listNodeParams := dvpn.ListNodeParams{Node: nodeAddress, Signer: nodeOwner.PublicKey()}
	listNodeIx, err := dvpn.BuildListNodeInstruction(localnet.ProgramID, listNodeParams)
	require.NoError(t, err)

	listNodeIxs := []solana.Instruction{listNodeIx}
	_, err = tx.Send(ctx, localnet.RPC, localnet.WS, nodeOwner, tx.DefaultConfig(), listNodeIxs)
	require.NoError(t, err)

	return nodeAddress
}

func startDaemon(
	t *testing.T,
	r *client.Runtime,
	dir string,
	reg *prometheus.Registry,
	roleReg prometheus.Registerer,
) *grpc.ClientConn {
	t.Helper()

	log := zerolog.Nop()
	server := daemon.NewServer(log, reg)
	runner, err := worker.New(log, roleReg)
	require.NoError(t, err)
	ln, err := daemon.Listen(t.Context(), filepath.Join(dir, daemonv1.SocketFileName), "")
	require.NoError(t, err)
	conn, err := grpc.NewClient(
		"unix:"+filepath.Join(dir, daemonv1.SocketFileName),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx, r, server, ln, runner, log, func() {}) }()

	t.Cleanup(func() {
		require.NoError(t, conn.Close())
		cancel()

		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Error("client daemon did not stop")
		}
	})
	waitServing(t, conn)

	return conn
}

func startFirstSession(t *testing.T, ctx context.Context, fixture *integrationFixture, svc *testutil.FakeNodeService) {
	t.Helper()

	control := daemonv1.NewClientServiceClient(fixture.conn)
	started, err := control.StartSession(ctx, newChannelRequest(fixture.nodeAddress.String()))
	require.NoError(t, err)

	sess := started.GetSession()
	require.Equal(t, client.SessionStatusActive, sess.GetStatus())
	require.Equal(t, 1, svc.OpenedCount())

	channels, err := control.ListChannels(ctx, &daemonv1.ListChannelsRequest{})
	require.NoError(t, err)
	require.Len(t, channels.GetChannels(), 1)
	require.Equal(t, client.ChannelStatusOpen, channels.GetChannels()[0].GetStatus())
	fixture.state.SetLocalStats(service.PeerStats{RxBytes: clientGrowthBytes})
	svc.SetUsage(session.Usage{RxBytes: clientGrowthBytes})
	require.Eventually(t, func() bool { return svc.VoucherCount() >= 1 }, 10*time.Second, 20*time.Millisecond)

	_, err = control.StopSession(ctx, &daemonv1.StopSessionRequest{})
	require.NoError(t, err)
	require.Equal(t, 1, svc.ClosedCount())
	require.Equal(t, sess.GetId(), svc.ClosedSessionID(0))
}

func waitServing(t *testing.T, conn *grpc.ClientConn) {
	t.Helper()

	health := healthpb.NewHealthClient(conn)

	require.Eventually(t, func() bool {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		response, err := health.Check(ctx, &healthpb.HealthCheckRequest{})

		return err == nil && response.GetStatus() == healthpb.HealthCheckResponse_SERVING
	}, 10*time.Second, 20*time.Millisecond)
}
