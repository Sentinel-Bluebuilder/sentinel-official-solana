package testutil

import (
	"bytes"
	"context"
	"math/rand/v2"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	bin "github.com/gagliardetto/binary"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-dvpnd/internal/rpcpin"
	"github.com/sentinel-official/sentinel-dvpnd/internal/store"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const (
	mintFixture      = "SentineLMint1111111111111111111111111111111"
	programIDFixture = "SentineLDVPN1111111111111111111111111111111"
)

type FakeNodeServerConfig struct {
	Name          service.Name
	Node          solana.PublicKey
	Owner         solana.PublicKey
	Port          uint16
	ProgramClient *program.Client
	ProgramID     solana.PublicKey
	Settlement    nodeapi.Settlement
	Signer        solana.PrivateKey
}

type Home struct {
	DaemonKey     solana.PrivateKey
	DB            *gorm.DB
	Loaded        *config.Loaded
	NodeAddress   solana.PublicKey
	NodeServer    *httptest.Server
	NodeService   *FakeNodeService
	OwnerAddress  solana.PublicKey
	OwnerKey      solana.PrivateKey
	Pin           *rpcpin.Pin
	ProgramClient *program.Client
	RPC           *sdktest.FakeRPC
	Sender        *tx.Sender
}

type RecordingInterfaceRemover struct {
	Err   error
	Names []string
}

func (r *RecordingInterfaceRemover) RemoveInterface(_ context.Context, name string) error {
	r.Names = append(r.Names, name)

	return r.Err
}

func ChecksByName(checks []service.PreflightCheck) map[string]service.PreflightCheck {
	byName := make(map[string]service.PreflightCheck, len(checks))
	for _, check := range checks {
		byName[check.Name] = check
	}

	return byName
}

func NewClientHome(t *testing.T, fakeRPC *sdktest.FakeRPC) Home {
	t.Helper()

	programID := solana.MustPublicKeyFromBase58(programIDFixture)
	mint := solana.MustPublicKeyFromBase58(mintFixture)
	sdktest.PinBuildIdentity(t, programID, mint)
	dir := t.TempDir()
	device := newPrivateKey(t)
	cfg := config.DefaultClient()
	cfg.Owner = device.PublicKey().String()

	seedConfigAccount(t, fakeRPC, programID, dvpn.Config{Mint: mint})
	nodeAddress, server, nodeService := startNodeAPI(t, fakeRPC, sdktest.FakeName)
	nodeService.SetSessionSigner(device.PublicKey())

	rpcCfg := fakeRPC.Config()
	loaded := &config.Loaded{
		Client: &cfg,
		Dir:    dir,
		File: config.File{
			Peer: cli.PeerConfig{AllowPrivate: true},
			RPC:  rpcCfg,
		},
		Role:     config.RoleClient,
		Root:     t.TempDir(),
		Services: map[service.Name]any{},
		Tag:      NewInstanceTag(t, config.RoleClient),
	}
	db, err := store.Open(filepath.Join(loaded.Dir, "data.db"))
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	pin, err := rpcpin.New(rpcCfg.URL, rpcCfg.Dialer())
	require.NoError(t, err)

	return Home{
		DaemonKey:     device,
		DB:            db,
		Loaded:        loaded,
		NodeAddress:   nodeAddress,
		NodeServer:    server,
		NodeService:   nodeService,
		OwnerAddress:  device.PublicKey(),
		Pin:           pin,
		ProgramClient: program.NewClient(fakeRPC.NewClient(), programID, mint, rpcCfg.Commitment),
		RPC:           fakeRPC,
		Sender:        tx.NewSender(fakeRPC.NewClient(), nil, tx.DefaultConfig(), zerolog.Nop()),
	}
}

func NewInstanceTag(t *testing.T, role string) instance.Tag {
	t.Helper()

	tag, err := instance.Parse(role[:1] + strconv.Itoa(rand.IntN(1_000_000_000)))
	require.NoError(t, err)

	return tag
}

func ParseOpenRentRecipients(t *testing.T, data []byte) []solana.PublicKey {
	t.Helper()

	var (
		body       []byte
		escrow     uint64
		id         [32]uint8
		recipients []solana.PublicKey
	)

	switch {
	case bytes.HasPrefix(data, dvpn.OpenChannelInstructionDiscriminator[:]):
		body, recipients = data[len(dvpn.OpenChannelInstructionDiscriminator):], make([]solana.PublicKey, 2)
	case bytes.HasPrefix(data, dvpn.OpenLeaseInstructionDiscriminator[:]):
		body, recipients = data[len(dvpn.OpenLeaseInstructionDiscriminator):], make([]solana.PublicKey, 1)
	default:
		require.FailNow(t, "instruction is neither open_channel nor open_lease")
	}

	decoder := bin.NewBorshDecoder(body)
	require.NoError(t, decoder.Decode(&id))
	require.NoError(t, decoder.Decode(&escrow))

	_, err := dvpn.DecodePositionTerms(decoder)
	require.NoError(t, err)

	for i := range recipients {
		require.NoError(t, decoder.Decode(&recipients[i]))
	}

	require.Zero(t, decoder.Remaining())

	return recipients
}

func StartFakeNodeServer(t *testing.T, cfg FakeNodeServerConfig) (*httptest.Server, *FakeNodeService) {
	t.Helper()

	var opts []FakeNodeOption
	if cfg.ProgramClient != nil {
		opts = append(opts, WithProgramClient(cfg.ProgramClient))
	}

	nodeService := NewFakeNodeService(cfg.Signer, uuid.NewString(), opts...)
	engine := api.NewEngine(api.EngineOptions{
		Logger:         zerolog.Nop(),
		StreamingPaths: nodeapi.StreamingPaths(),
	})
	nodeapi.RegisterRoutes(
		engine,
		nodeService,
		auth.NewNonceCache(auth.RequestNonceWindow),
		api.NewLimiter(100, 100),
		api.NewLimiter(100, 100),
	)

	server := httptest.NewUnstartedServer(engine)
	require.NoError(t, server.Listener.Close())

	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:"+strconv.Itoa(int(cfg.Port)))
	require.NoError(t, err)

	server.Listener = listener
	server.StartTLS()
	t.Cleanup(server.Close)

	now := time.Now()
	nodeService.SetMetadata(nodeapi.Metadata{
		API: nodeapi.APIInfo{
			TLSSHA256: api.Fingerprint(server.Certificate()),
			URLs:      []string{server.URL},
		},
		ExpiresAt:  now.Add(auth.MetadataTTL).Unix(),
		IssuedAt:   now.Unix(),
		Location:   nodeapi.Location{Country: "DE"},
		Node:       cfg.Node,
		Operator:   cfg.Signer.PublicKey(),
		Owner:      cfg.Owner,
		ProgramID:  cfg.ProgramID,
		SDKVersion: "test",
		Service: nodeapi.ServiceMetadata{
			Name:    cfg.Name,
			Payload: []byte(`{"port":51820}`),
		},
		Settlement: cfg.Settlement,
		Version:    auth.MetadataVersion,
	})

	return server, nodeService
}

func newPrivateKey(t *testing.T) solana.PrivateKey {
	t.Helper()

	privateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	return privateKey
}

func packURI(value string) ([128]uint8, uint8) {
	var uri [128]uint8
	copy(uri[:], value)

	return uri, uint8(len(value))
}

func seedConfigAccount(t *testing.T, fakeRPC *sdktest.FakeRPC, programID solana.PublicKey, chainCfg dvpn.Config) {
	t.Helper()
	require.NoError(t, fakeRPC.SetConfig(programID, chainCfg))
}

func seedGrantAccount(
	t *testing.T,
	fakeRPC *sdktest.FakeRPC,
	programID, owner, operator solana.PublicKey,
	permissions uint64,
) {
	t.Helper()
	require.NoError(t, fakeRPC.SetGrant(programID, owner, operator, solana.PublicKey{}, permissions))
}

func seedNodeAccount(
	t *testing.T,
	fakeRPC *sdktest.FakeRPC,
	programID, nodeAddress, owner solana.PublicKey,
	nodeURL string,
) {
	t.Helper()

	uri, uriLen := packURI(nodeURL)
	prices := defaultFakeSettlement().Prices
	fakeRPC.SetNode(programID, nodeAddress, dvpn.Node{
		ChannelPricePerGB:   prices.ChannelPricePerGB,
		ChannelPricePerHour: prices.ChannelPricePerHour,
		LeasePricePerHour:   prices.LeasePricePerHour,
		OpenedAt:            time.Now().Unix(),
		Owner:               owner,
		Status:              dvpn.ListingStatusListed,
		URI:                 uri,
		URILen:              uriLen,
	})
}

func startNodeAPI(
	t *testing.T,
	fakeRPC *sdktest.FakeRPC,
	name service.Name,
) (solana.PublicKey, *httptest.Server, *FakeNodeService) {
	t.Helper()

	programID := solana.MustPublicKeyFromBase58(programIDFixture)
	owner := newPrivateKey(t)
	operator := newPrivateKey(t)
	nodeAddress := newPrivateKey(t).PublicKey()
	server, nodeService := StartFakeNodeServer(t, FakeNodeServerConfig{
		Name:       name,
		Node:       nodeAddress,
		Owner:      owner.PublicKey(),
		ProgramID:  programID,
		Settlement: defaultFakeSettlement(),
		Signer:     operator,
	})
	seedNodeAccount(t, fakeRPC, programID, nodeAddress, owner.PublicKey(), server.URL)
	seedGrantAccount(t, fakeRPC, programID, owner.PublicKey(), operator.PublicKey(), program.PermsNode)

	return nodeAddress, server, nodeService
}
