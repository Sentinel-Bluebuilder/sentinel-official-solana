//go:build integration && linux

package provider_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	token2022 "github.com/solana-foundation/solana-go/v2/programs/token-2022"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-dvpnd/internal/daemon"
	"github.com/sentinel-official/sentinel-dvpnd/internal/provider"
	"github.com/sentinel-official/sentinel-dvpnd/internal/store"
	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	"github.com/sentinel-official/sentinel-dvpnd/internal/worker"
	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
	"github.com/sentinel-official/sentinel-go-sdk/v3/wireguard"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const (
	providerBondLamports      = 15_000_000_000
	providerFundTokens        = 30_000_000_000
	providerLeasePricePerHour = 200_000
	providerPlanDataCapGB     = 10
	providerPlanDurationSec   = 3_600
	providerSOL               = 5
)

type integrationFixture struct {
	conn            *grpc.ClientConn
	localnet        *sdktest.Localnet
	nodeAddress     solana.PublicKey
	nodeService     *testutil.FakeNodeService
	owner           solana.PrivateKey
	planAddress     solana.PublicKey
	programClient   *program.Client
	providerAddress solana.PublicKey
	runtime         *provider.Runtime
}

func newIntegrationFixture(t *testing.T) *integrationFixture {
	t.Helper()
	localnet := sdktest.StartLocalnet(t)
	programClient := program.NewClient(localnet.RPC, localnet.ProgramID, localnet.Mint, rpc.CommitmentConfirmed)
	owner, operator, nodeOwner, id, providerAddress := openProviderOnChain(t, localnet, programClient)
	port, err := service.FreePort()
	require.NoError(t, err)
	planAddress, nodeAddress := openPlanAndNode(
		t, localnet, programClient, providerAddress, owner, nodeOwner, operator, port,
	)
	_, nodeService := testutil.StartFakeNodeServer(t, testutil.FakeNodeServerConfig{
		Name:          wireguard.Name,
		Node:          nodeAddress,
		Owner:         nodeOwner.PublicKey(),
		Port:          port,
		ProgramClient: programClient,
		ProgramID:     localnet.ProgramID,
		Settlement: nodeapi.Settlement{
			HeartbeatSeconds:        1,
			MinChannelPrepayBytes:   1,
			MinChannelPrepaySeconds: 1,
			MinLeasePrepaySeconds:   3_600,
			Prices:                  nodeapi.Prices{LeasePricePerHour: providerLeasePricePerHour},
		},
		Signer: operator,
	})
	home := t.TempDir()
	cfg := config.DefaultProvider()
	cfg.API.Hosts = []string{"127.0.0.1"}
	cfg.API.Port = 0
	cfg.ID = hex.EncodeToString(id[:])
	cfg.Lease.ReserveHours = 1
	cfg.Lease.TopupHours = 1
	cfg.Owner = owner.PublicKey().String()
	loaded := &config.Loaded{
		Dir:      home,
		File:     config.File{Peer: cli.PeerConfig{AllowPrivate: true}},
		Provider: &cfg,
		Role:     config.RoleProvider,
		Tag:      testutil.NewInstanceTag(t, config.RoleProvider),
	}
	db, err := store.Open(filepath.Join(loaded.Dir, "data.db"))
	require.NoError(t, err)

	reg := daemon.NewRegistry()
	roleReg := prometheus.WrapRegistererWith(prometheus.Labels{"role": config.RoleProvider}, reg)
	log := zerolog.Nop()
	sender := tx.NewSender(localnet.RPC, nil, tx.DefaultConfig(), log)
	rt, err := provider.New(loaded, db, operator, programClient, sender, log, roleReg)
	require.NoError(t, err)
	require.NoError(t, store.Migrate(t.Context(), db, rt.Models()...))

	server := daemon.NewServer(log, reg)
	runner, err := worker.New(log, roleReg)
	require.NoError(t, err)

	socketPath := filepath.Join(home, daemonv1.SocketFileName)
	listener, err := daemon.Listen(t.Context(), socketPath, "")
	require.NoError(t, err)
	conn, err := grpc.NewClient("unix:"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx, rt, server, listener, runner, log, func() {}) }()

	t.Cleanup(func() {
		cancel()

		select {
		case runErr := <-done:
			require.NoError(t, runErr)
		case <-time.After(10 * time.Second):
			t.Error("provider daemon did not stop after cancel")
		}
	})
	waitServing(t, conn)

	return &integrationFixture{
		conn: conn, localnet: localnet, nodeAddress: nodeAddress, nodeService: nodeService, owner: owner,
		planAddress: planAddress, programClient: programClient, providerAddress: providerAddress, runtime: rt,
	}
}

//nolint:paralleltest
func TestRuntime_Localnet(t *testing.T) {
	fixture := newIntegrationFixture(t)
	client := daemonv1.NewProviderServiceClient(fixture.conn)
	_, err := client.AddRosterNodes(t.Context(), &daemonv1.AddRosterNodesRequest{
		Nodes: []string{fixture.nodeAddress.String()}, Plan: fixture.planAddress.String(),
	})
	require.NoError(t, err)
	listed, err := client.ListRosterNodes(t.Context(), &daemonv1.ListRosterNodesRequest{})
	require.NoError(t, err)
	require.Len(t, listed.GetNodes(), 1)
	require.Equal(t, fixture.planAddress.String(), listed.GetNodes()[0].GetPlan())
	_, err = client.KeepNow(t.Context(), &daemonv1.KeepNowRequest{})
	require.NoError(t, err)
	_, err = client.KeepNow(t.Context(), &daemonv1.KeepNowRequest{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return fixture.nodeService.LeaseVoucherCount() >= 1
	}, 20*time.Second, 50*time.Millisecond)
	leases, err := client.ListLeases(t.Context(), &daemonv1.ListLeasesRequest{})
	require.NoError(t, err)
	require.Len(t, leases.GetLeases(), 1)
	require.Equal(t, fixture.nodeAddress.String(), leases.GetLeases()[0].GetNode())
	require.Equal(t, provider.StatusOpen, leases.GetLeases()[0].GetStatus())
	require.True(t, leases.GetLeases()[0].GetHealthy())
	issueTestTicket(t, fixture)
}

func associatedTokenAddress(t *testing.T, programClient *program.Client, owner solana.PublicKey) solana.PublicKey {
	t.Helper()

	address, err := programClient.AssociatedTokenAddress(owner)
	require.NoError(t, err)

	return address
}

func issueTestTicket(t *testing.T, fixture *integrationFixture) {
	t.Helper()
	ctx := t.Context()
	userKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	openIx, err := dvpn.BuildOpenSubscriptionInstruction(fixture.localnet.ProgramID, dvpn.OpenSubscriptionParams{
		ExpectedDataCapGB:         providerPlanDataCapGB,
		ExpectedDurationSeconds:   providerPlanDurationSec,
		ExpectedMaxConnections:    3,
		Owner:                     userKey.PublicKey(),
		Plan:                      fixture.planAddress,
		Provider:                  fixture.providerAddress,
		RentPayer:                 fixture.owner.PublicKey(),
		Signer:                    fixture.owner.PublicKey(),
		SubscriptionRentRecipient: fixture.owner.PublicKey(),
	})
	require.NoError(t, err)
	_, err = tx.Send(
		ctx, fixture.localnet.RPC, fixture.localnet.WS, fixture.owner, tx.DefaultConfig(), []solana.Instruction{openIx},
	)
	require.NoError(t, err)
	subAddress, _, err := dvpn.FindSubscriptionAddress(
		fixture.localnet.ProgramID, userKey.PublicKey(), fixture.planAddress,
	)
	require.NoError(t, err)
	encoded := encodeTicketRequest(t, userKey, fixture.providerAddress, subAddress, fixture.nodeAddress, time.Now().Unix())
	result, err := provider.IssueTicket(ctx, fixture.runtime, encoded)
	require.NoError(t, err)
	require.NotEmpty(t, result.Ticket)
	ticket, err := auth.DecodeTicket(result.Ticket)
	require.NoError(t, err)
	require.NoError(t, auth.VerifyTicket(
		ctx, fixture.programClient, ticket, fixture.owner.PublicKey(), fixture.nodeAddress, time.Now(),
	))
}

func openPlanAndNode(
	t *testing.T,
	localnet *sdktest.Localnet,
	programClient *program.Client,
	providerAddress solana.PublicKey,
	owner, nodeOwner, operator solana.PrivateKey,
	port uint16,
) (solana.PublicKey, solana.PublicKey) {
	t.Helper()
	ctx := t.Context()
	planID := randomID32(t)
	openPlan, err := dvpn.BuildOpenPlanInstruction(localnet.ProgramID, dvpn.OpenPlanParams{
		DataCapGB:         providerPlanDataCapGB,
		DurationSeconds:   providerPlanDurationSec,
		MaxConnections:    3,
		PlanID:            planID,
		PlanRentRecipient: owner.PublicKey(),
		Price:             0,
		Provider:          providerAddress,
		RentPayer:         owner.PublicKey(),
		Signer:            owner.PublicKey(),
	})
	require.NoError(t, err)
	planAddress, _, err := dvpn.FindPlanAddress(localnet.ProgramID, providerAddress, planID)
	require.NoError(t, err)
	listPlan, err := dvpn.BuildListPlanInstruction(localnet.ProgramID, dvpn.ListPlanParams{
		Plan: planAddress, Provider: providerAddress, Signer: owner.PublicKey(),
	})
	require.NoError(t, err)
	_, err = tx.Send(ctx, localnet.RPC, localnet.WS, owner, tx.DefaultConfig(), []solana.Instruction{openPlan, listPlan})
	require.NoError(t, err)
	nodeID := randomID32(t)
	vault, err := programClient.VaultAddress()
	require.NoError(t, err)
	openNode, err := dvpn.BuildOpenNodeInstruction(localnet.ProgramID, dvpn.OpenNodeParams{
		Bond:              providerBondLamports,
		Country:           [2]uint8{'D', 'E'},
		Mint:              localnet.Mint,
		NodeID:            nodeID,
		NodeRentRecipient: nodeOwner.PublicKey(),
		Owner:             nodeOwner.PublicKey(),
		OwnerToken:        associatedTokenAddress(t, programClient, nodeOwner.PublicKey()),
		Prices:            dvpn.NodePrices{LeasePricePerHour: providerLeasePricePerHour},
		Protocols:         wireguard.Name.Bit(),
		RentPayer:         nodeOwner.PublicKey(),
		Signer:            nodeOwner.PublicKey(),
		URI:               []byte("https://127.0.0.1:" + strconv.Itoa(int(port))),
		Vault:             vault,
	})
	require.NoError(t, err)
	nodeAddress, _, err := dvpn.FindNodeAddress(localnet.ProgramID, nodeOwner.PublicKey(), nodeID)
	require.NoError(t, err)
	listNode, err := dvpn.BuildListNodeInstruction(localnet.ProgramID, dvpn.ListNodeParams{
		Node: nodeAddress, Signer: nodeOwner.PublicKey(),
	})
	require.NoError(t, err)
	_, err = tx.Send(
		ctx, localnet.RPC, localnet.WS, nodeOwner, tx.DefaultConfig(), []solana.Instruction{openNode, listNode},
	)
	require.NoError(t, err)
	openGrant, err := dvpn.BuildOpenGrantInstruction(localnet.ProgramID, dvpn.OpenGrantParams{
		Allowance:          dvpn.AllowanceUnlimited,
		ExpiresAt:          0,
		Grantee:            operator.PublicKey(),
		Granter:            nodeOwner.PublicKey(),
		GrantRentRecipient: nodeOwner.PublicKey(),
		Permissions:        program.PermSignNodeMetadata,
		RentPayer:          nodeOwner.PublicKey(),
		Target:             solana.PublicKey{},
	})
	require.NoError(t, err)
	_, err = tx.Send(ctx, localnet.RPC, localnet.WS, nodeOwner, tx.DefaultConfig(), []solana.Instruction{openGrant})
	require.NoError(t, err)

	return planAddress, nodeAddress
}

func openProviderOnChain(
	t *testing.T, localnet *sdktest.Localnet, programClient *program.Client,
) (solana.PrivateKey, solana.PrivateKey, solana.PrivateKey, [32]uint8, solana.PublicKey) {
	t.Helper()
	ctx := t.Context()
	owner, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	require.NoError(t, localnet.FundSOL(ctx, owner.PublicKey(), providerSOL))
	require.NoError(t, localnet.FundTokens(ctx, owner.PublicKey(), providerFundTokens))

	operator, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	require.NoError(t, localnet.FundSOL(ctx, operator.PublicKey(), providerSOL))

	nodeOwner, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	require.NoError(t, localnet.FundSOL(ctx, nodeOwner.PublicKey(), providerSOL))
	require.NoError(t, localnet.FundTokens(ctx, nodeOwner.PublicKey(), providerFundTokens))
	providerID := randomID32(t)
	vault, err := programClient.VaultAddress()
	require.NoError(t, err)
	openIx, err := dvpn.BuildOpenProviderInstruction(localnet.ProgramID, dvpn.OpenProviderParams{
		Bond:                  providerBondLamports,
		Mint:                  localnet.Mint,
		Owner:                 owner.PublicKey(),
		OwnerToken:            associatedTokenAddress(t, programClient, owner.PublicKey()),
		ProviderID:            providerID,
		ProviderRentRecipient: owner.PublicKey(),
		RentPayer:             owner.PublicKey(),
		Signer:                owner.PublicKey(),
		URI:                   []byte("https://127.0.0.1:0"),
		Vault:                 vault,
	})
	require.NoError(t, err)
	providerAddress, _, err := dvpn.FindProviderAddress(localnet.ProgramID, owner.PublicKey(), providerID)
	require.NoError(t, err)
	listIx, err := dvpn.BuildListProviderInstruction(localnet.ProgramID, dvpn.ListProviderParams{
		Provider: providerAddress, Signer: owner.PublicKey(),
	})
	require.NoError(t, err)
	_, err = tx.Send(ctx, localnet.RPC, localnet.WS, owner, tx.DefaultConfig(), []solana.Instruction{openIx, listIx})
	require.NoError(t, err)
	openGrant, err := dvpn.BuildOpenGrantInstruction(localnet.ProgramID, dvpn.OpenGrantParams{
		Allowance:          dvpn.AllowanceUnlimited,
		ExpiresAt:          0,
		Grantee:            operator.PublicKey(),
		Granter:            owner.PublicKey(),
		GrantRentRecipient: owner.PublicKey(),
		Permissions:        program.PermsProvider,
		RentPayer:          owner.PublicKey(),
		Target:             solana.PublicKey{},
	})
	require.NoError(t, err)
	chainCfg, err := programClient.Config(ctx)
	require.NoError(t, err)
	configAddress, _, err := dvpn.FindConfigAddress(localnet.ProgramID)
	require.NoError(t, err)
	ownerToken := associatedTokenAddress(t, programClient, owner.PublicKey())
	approve := token2022.NewApproveCheckedInstruction(
		providerFundTokens, chainCfg.Decimals, ownerToken, localnet.Mint, configAddress, owner.PublicKey(), nil,
	).Build()
	_, err = tx.Send(ctx, localnet.RPC, localnet.WS, owner, tx.DefaultConfig(), []solana.Instruction{openGrant, approve})
	require.NoError(t, err)

	return owner, operator, nodeOwner, providerID, providerAddress
}

func randomID32(t *testing.T) [32]uint8 {
	t.Helper()

	var id [32]uint8

	_, err := rand.Read(id[:])
	require.NoError(t, err)

	return id
}

func waitServing(t *testing.T, conn *grpc.ClientConn) {
	t.Helper()

	client := grpc_health_v1.NewHealthClient(conn)

	require.Eventually(t, func() bool {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		response, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{})

		return err == nil && response.GetStatus() == grpc_health_v1.HealthCheckResponse_SERVING
	}, 30*time.Second, 100*time.Millisecond)
}
