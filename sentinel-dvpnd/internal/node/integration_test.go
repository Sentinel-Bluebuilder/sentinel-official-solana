//go:build integration && linux

package node_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
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

	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-dvpnd/internal/daemon"
	"github.com/sentinel-official/sentinel-dvpnd/internal/node"
	"github.com/sentinel-official/sentinel-dvpnd/internal/rpcpin"
	"github.com/sentinel-official/sentinel-dvpnd/internal/store"
	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	"github.com/sentinel-official/sentinel-dvpnd/internal/worker"
	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
	"github.com/sentinel-official/sentinel-go-sdk/v3/voucher"
	"github.com/sentinel-official/sentinel-go-sdk/v3/wireguard"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const (
	integrationBondLamports  = 5_000_000_000
	integrationChannelEscrow = 5_000_000_000
	integrationChannelPrice  = 5_000
	integrationFundTokens    = 20_000_000_000
	integrationPrepayBytes   = 500_000_000
	integrationSOL           = 5
	wsTimeout                = 10 * time.Second
)

type integrationFixture struct {
	conn          *grpc.ClientConn
	db            *gorm.DB
	localnet      *sdktest.Localnet
	nodeAddress   solana.PublicKey
	owner         solana.PrivateKey
	programClient *program.Client
	runtime       *node.Runtime
	state         *sdktest.FakeState
}

func newIntegrationFixture(t *testing.T, localnet *sdktest.Localnet) *integrationFixture {
	t.Helper()

	programClient := program.NewClient(localnet.RPC, localnet.ProgramID, localnet.Mint, rpc.CommitmentConfirmed)
	port, err := service.FreePort()
	require.NoError(t, err)
	owner, operator, nodeID, nodeAddress := openNodeOnChain(t, localnet, programClient, port)
	dir := t.TempDir()
	cfg := config.DefaultNode()
	cfg.API.Hosts = []string{"127.0.0.1"}
	cfg.API.Port = port
	cfg.Country = "DE"
	cfg.ID = hex.EncodeToString(nodeID[:])
	cfg.Owner = owner.PublicKey().String()
	cfg.Prices.ChannelPerGB = integrationChannelPrice
	cfg.Redeem.ThresholdBaseUnits = 1
	cfg.Service = wireguard.Name.String()
	cfg.Speedtest.Enabled = false
	loaded := &config.Loaded{
		Dir:      dir,
		Node:     &cfg,
		Role:     config.RoleNode,
		Root:     t.TempDir(),
		Services: map[service.Name]any{wireguard.Name: wireguard.DefaultServerConfig()},
		Tag:      testutil.NewInstanceTag(t, config.RoleNode),
	}
	db, err := store.Open(filepath.Join(loaded.Dir, "data.db"))
	require.NoError(t, err)

	reg := prometheus.NewRegistry()
	roleReg := prometheus.WrapRegistererWith(prometheus.Labels{"role": config.RoleNode}, reg)
	log := zerolog.Nop()
	sender := tx.NewSender(
		localnet.RPC, rpcpin.NewWSDialer(localnet.WSURL, wsTimeout, nil, log), tx.DefaultConfig(), log,
	)
	r, err := node.New(loaded, db, operator, programClient, sender, log, roleReg)
	require.NoError(t, err)
	require.NoError(t, store.Migrate(t.Context(), db, r.Models()...))

	state := sdktest.NewFakeState(wireguard.Name)
	node.SetService(r, sdktest.NewFakeServer(state))
	node.SetInterfaceRemover(r, &testutil.RecordingInterfaceRemover{})
	conn := startDaemon(t, r, reg, roleReg)

	return &integrationFixture{
		conn: conn, db: db, localnet: localnet, nodeAddress: nodeAddress, owner: owner, programClient: programClient,
		runtime: r, state: state,
	}
}

//nolint:paralleltest
func TestRuntime_Localnet(t *testing.T) {
	localnet := sdktest.StartLocalnet(t)
	fixture := newIntegrationFixture(t, localnet)
	client := daemonv1.NewNodeServiceClient(fixture.conn)
	_, err := client.SyncConfigNow(t.Context(), &daemonv1.SyncConfigNowRequest{})
	require.NoError(t, err)
	syncedNode, err := fixture.programClient.FetchNode(t.Context(), fixture.nodeAddress)
	require.NoError(t, err)
	require.Equal(t, uint64(integrationChannelPrice), syncedNode.Value.ChannelPricePerGB)
	checkHeartbeat(t, fixture)
	channelAddress, device := openTestChannel(t, fixture)
	sessionID := openTestSession(t, fixture, channelAddress, device)
	checkUsage(t, fixture, sessionID)
	checkRedeem(t, fixture, channelAddress)
}

func associatedTokenAddress(t *testing.T, programClient *program.Client, owner solana.PublicKey) solana.PublicKey {
	t.Helper()

	address, err := programClient.AssociatedTokenAddress(owner)
	require.NoError(t, err)

	return address
}

func checkHeartbeat(t *testing.T, fixture *integrationFixture) {
	t.Helper()

	client := daemonv1.NewNodeServiceClient(fixture.conn)
	before, err := client.GetNodeInfo(t.Context(), &daemonv1.GetNodeInfoRequest{})
	require.NoError(t, err)
	require.Equal(t, "listed", before.GetInfo().GetStatus())
	listedBefore := before.GetInfo().GetListedAt()
	chainCfg, chainNode := node.ChainState(fixture.runtime)
	chainNode.ListedAt = time.Now().Unix() - (int64(chainCfg.NodeListingTTLSeconds) - 1_800)
	node.SetChainNode(fixture.runtime, chainNode)

	_, err = client.HeartbeatNow(t.Context(), &daemonv1.HeartbeatNowRequest{})
	require.NoError(t, err)
	runJob(t, fixture.runtime, "refresh_metadata")
	after, err := client.GetNodeInfo(t.Context(), &daemonv1.GetNodeInfoRequest{})
	require.NoError(t, err)
	require.GreaterOrEqual(t, after.GetInfo().GetListedAt(), listedBefore)
	require.Equal(t, "listed", after.GetInfo().GetStatus())
}

func checkRedeem(t *testing.T, fixture *integrationFixture, channelAddress solana.PublicKey) {
	t.Helper()

	client := daemonv1.NewNodeServiceClient(fixture.conn)
	_, err := client.RedeemNow(t.Context(), &daemonv1.RedeemNowRequest{})
	require.NoError(t, err)
	listed, err := client.ListRedemptions(t.Context(), &daemonv1.ListRedemptionsRequest{})
	require.NoError(t, err)

	var redemption *daemonv1.Redemption

	for _, row := range listed.GetRedemptions() {
		if row.GetKind() == node.KindChannel+"/0" {
			redemption = row

			break
		}
	}

	require.NotNil(t, redemption)
	require.Equal(t, node.RedemptionStatusConfirmed, redemption.GetStatus())
	signature, err := solana.SignatureFromBase58(redemption.GetSignature())
	require.NoError(t, err)

	maxVersion := rpc.MaxSupportedTransactionVersion1
	opts := &rpc.GetTransactionOpts{
		Commitment:                     rpc.CommitmentConfirmed,
		MaxSupportedTransactionVersion: &maxVersion,
	}
	result, err := fixture.localnet.RPC.GetTransaction(t.Context(), signature, opts)
	require.NoError(t, err)
	require.NotNil(t, result.Meta)
	events := dvpn.ParseEvents(result.Meta.LogMessages)

	var redeemed *dvpn.ChannelVoucherRedeemed

	for _, event := range events {
		if value, ok := event.(*dvpn.ChannelVoucherRedeemed); ok {
			redeemed = value

			break
		}
	}

	require.NotNil(t, redeemed)
	require.Equal(t, channelAddress, redeemed.Channel)
	require.Equal(t, fixture.nodeAddress, redeemed.Node)
}

func checkUsage(t *testing.T, fixture *integrationFixture, sessionID string) {
	t.Helper()

	client := daemonv1.NewNodeServiceClient(fixture.conn)
	before, err := client.GetNodeSession(t.Context(), &daemonv1.GetNodeSessionRequest{Id: sessionID})
	require.NoError(t, err)
	require.NotEmpty(t, before.GetSession().GetPeerId())

	stats := service.PeerStats{RxBytes: 10_000_000, TxBytes: 2_000_000}
	fixture.state.SetStats(service.PeerID(before.GetSession().GetPeerId()), stats)
	runJob(t, fixture.runtime, "sync_usage")
	after, err := client.GetNodeSession(t.Context(), &daemonv1.GetNodeSessionRequest{Id: sessionID})
	require.NoError(t, err)
	require.Equal(t, stats.RxBytes, after.GetSession().GetUsage().GetRxBytes())
	require.Equal(t, stats.TxBytes, after.GetSession().GetUsage().GetTxBytes())
}

func nodeBaseURL(port uint16) string { return fmt.Sprintf("https://127.0.0.1:%d", port) }

func openNodeOnChain(
	t *testing.T,
	localnet *sdktest.Localnet,
	programClient *program.Client,
	apiPort uint16,
) (solana.PrivateKey, solana.PrivateKey, [32]uint8, solana.PublicKey) {
	t.Helper()
	ctx := t.Context()
	owner, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	require.NoError(t, localnet.FundSOL(ctx, owner.PublicKey(), integrationSOL))
	require.NoError(t, localnet.FundTokens(ctx, owner.PublicKey(), integrationFundTokens))

	operator, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	require.NoError(t, localnet.FundSOL(ctx, operator.PublicKey(), integrationSOL))

	var nodeID [32]uint8

	_, err = rand.Read(nodeID[:])
	require.NoError(t, err)
	vault, err := programClient.VaultAddress()
	require.NoError(t, err)
	openNode, err := dvpn.BuildOpenNodeInstruction(localnet.ProgramID, dvpn.OpenNodeParams{
		Bond:              integrationBondLamports,
		Country:           [2]uint8{'D', 'E'},
		Mint:              programClient.Mint,
		NodeID:            nodeID,
		NodeRentRecipient: owner.PublicKey(),
		Owner:             owner.PublicKey(),
		OwnerToken:        associatedTokenAddress(t, programClient, owner.PublicKey()),
		Prices:            dvpn.NodePrices{ChannelPricePerGB: integrationChannelPrice},
		Protocols:         wireguard.Name.Bit(),
		RentPayer:         owner.PublicKey(),
		Signer:            owner.PublicKey(),
		URI:               []byte(nodeBaseURL(apiPort)),
		Vault:             vault,
	})
	require.NoError(t, err)
	_, err = tx.Send(ctx, localnet.RPC, localnet.WS, owner, tx.DefaultConfig(), []solana.Instruction{openNode})
	require.NoError(t, err)
	openGrant, err := dvpn.BuildOpenGrantInstruction(localnet.ProgramID, dvpn.OpenGrantParams{
		Allowance:          dvpn.AllowanceUnlimited,
		ExpiresAt:          0,
		Grantee:            operator.PublicKey(),
		Granter:            owner.PublicKey(),
		GrantRentRecipient: owner.PublicKey(),
		Permissions:        program.PermsNode,
		RentPayer:          owner.PublicKey(),
		Target:             solana.PublicKey{},
	})
	require.NoError(t, err)
	_, err = tx.Send(ctx, localnet.RPC, localnet.WS, owner, tx.DefaultConfig(), []solana.Instruction{openGrant})
	require.NoError(t, err)
	nodeAddress, _, err := dvpn.FindNodeAddress(localnet.ProgramID, owner.PublicKey(), nodeID)
	require.NoError(t, err)

	return owner, operator, nodeID, nodeAddress
}

func openTestChannel(t *testing.T, fixture *integrationFixture) (solana.PublicKey, solana.PrivateKey) {
	t.Helper()
	ctx := t.Context()
	device, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	require.NoError(t, fixture.localnet.FundSOL(ctx, device.PublicKey(), integrationSOL))
	require.NoError(t, fixture.localnet.FundTokens(ctx, device.PublicKey(), integrationFundTokens))

	var channelID [32]uint8

	_, err = rand.Read(channelID[:])
	require.NoError(t, err)
	vault, err := fixture.programClient.VaultAddress()
	require.NoError(t, err)

	instruction, err := dvpn.BuildOpenChannelInstruction(fixture.localnet.ProgramID, dvpn.OpenChannelParams{
		ChannelID:            channelID,
		ChannelRentRecipient: device.PublicKey(),
		Escrow:               integrationChannelEscrow,
		Mint:                 fixture.programClient.Mint,
		Node:                 fixture.nodeAddress,
		Owner:                device.PublicKey(),
		OwnerToken:           associatedTokenAddress(t, fixture.programClient, device.PublicKey()),
		RentPayer:            device.PublicKey(),
		Signer:               device.PublicKey(),
		Terms:                &dvpn.PositionTermsVoucher{VoucherSigner: device.PublicKey()},
		UserRentRecipient:    device.PublicKey(),
		Vault:                vault,
	})
	require.NoError(t, err)
	_, err = tx.Send(
		ctx, fixture.localnet.RPC, fixture.localnet.WS, device, tx.DefaultConfig(), []solana.Instruction{instruction},
	)
	require.NoError(t, err)
	address, _, err := dvpn.FindChannelAddress(fixture.localnet.ProgramID, device.PublicKey(), channelID)
	require.NoError(t, err)

	return address, device
}

func openTestSession(
	t *testing.T,
	fixture *integrationFixture,
	channelAddress solana.PublicKey,
	device solana.PrivateKey,
) string {
	t.Helper()
	ctx := t.Context()
	account, err := fixture.programClient.FetchChannel(ctx, channelAddress)
	require.NoError(t, err)

	chainChannel := account.Value
	terms := session.Terms{
		Basis: session.BasisPerGB, Price: integrationChannelPrice,
	}
	owed := session.Owed(terms.Basis, terms.Price, integrationPrepayBytes)
	message := voucher.ChannelMessage(channelAddress, chainChannel.OpenedAt, owed)
	signedVoucher := voucher.Sign(device, message)
	chainNode, err := fixture.programClient.FetchNode(ctx, fixture.nodeAddress)
	require.NoError(t, err)
	metadata, uri, err := nodeapi.Discover(
		ctx, fixture.programClient, fixture.nodeAddress, chainNode.Value, 15*time.Second,
	)
	require.NoError(t, err)
	pinned, err := nodeapi.NewPinnedClient(uri.String(), metadata.API.TLSSHA256, device, 15*time.Second)
	require.NoError(t, err)

	peer := nodeapi.PeerRequest{
		Name: wireguard.Name, Payload: []byte(`{"public_key":"device"}`),
	}
	credential := nodeapi.Credential{Channel: &channelAddress, Type: nodeapi.CredentialTypeChannel}
	voucherBody := nodeapi.Voucher{Cumulative: signedVoucher.CumulativeAmount, Signature: signedVoucher.Signature[:]}
	body := nodeapi.OpenSessionRequest{Credential: credential, Peer: peer, Terms: &terms, Voucher: &voucherBody}
	response, err := pinned.OpenSessionWithResponse(ctx, body)
	require.NoError(t, err)
	require.NotNil(t, response.JSON200, "status %d: %s", response.StatusCode(), response.Body)
	require.NotEmpty(t, response.JSON200.SessionID)

	return response.JSON200.SessionID
}

func runJob(t *testing.T, r *node.Runtime, name string) {
	t.Helper()

	for _, job := range r.Jobs() {
		if job.Name == name {
			require.NoError(t, job.Run(t.Context()))

			return
		}
	}

	t.Fatalf("job %s not registered", name)
}

func startDaemon(
	t *testing.T,
	r *node.Runtime,
	reg *prometheus.Registry,
	roleReg prometheus.Registerer,
) *grpc.ClientConn {
	t.Helper()
	path := filepath.Join(t.TempDir(), daemonv1.SocketFileName)
	listener, err := daemon.Listen(t.Context(), path, "")
	require.NoError(t, err)

	log := zerolog.Nop()
	server := daemon.NewServer(log, reg)
	runner, err := worker.New(log, roleReg)
	require.NoError(t, err)
	conn, err := grpc.NewClient("unix://"+path, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx, r, server, listener, runner, log, func() {}) }()

	t.Cleanup(func() {
		cancel()

		select {
		case runErr := <-done:
			require.NoError(t, runErr)
		case <-time.After(10 * time.Second):
			t.Error("node daemon did not stop after cancel")
		}

		_ = conn.Close()
	})
	waitServing(t, conn)

	return conn
}

func waitServing(t *testing.T, conn *grpc.ClientConn) {
	t.Helper()

	client := healthpb.NewHealthClient(conn)

	require.Eventually(t, func() bool {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		response, err := client.Check(ctx, &healthpb.HealthCheckRequest{})

		return err == nil && response.GetStatus() == healthpb.HealthCheckResponse_SERVING
	}, 30*time.Second, 100*time.Millisecond)
}
