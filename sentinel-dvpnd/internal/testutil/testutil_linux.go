package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-dvpnd/internal/store"
	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/key"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
	"github.com/sentinel-official/sentinel-go-sdk/v3/wireguard"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func NewNodeHome(t *testing.T, fakeRPC *sdktest.FakeRPC) Home {
	t.Helper()

	programID := solana.MustPublicKeyFromBase58(programIDFixture)
	mint := solana.MustPublicKeyFromBase58(mintFixture)
	sdktest.PinBuildIdentity(t, programID, mint)
	dir := t.TempDir()
	operator := newPrivateKey(t)
	owner := newPrivateKey(t)
	cfg := config.DefaultNode()
	cfg.Country = "DE"
	cfg.ID = strings.Repeat("a", 64)
	cfg.Owner = owner.PublicKey().String()
	cfg.API.Hosts = []string{"203.0.113.1"}
	cfg.Service = wireguard.Name.String()
	port, err := service.FreePort()
	require.NoError(t, err)

	cfg.API.Port = port
	loaded := &config.Loaded{
		Dir: dir,
		File: config.File{
			Peer: cli.PeerConfig{AllowPrivate: true},
			RPC:  fakeRPC.Config(),
			Tx:   tx.DefaultConfig(),
		},
		Node:     &cfg,
		Role:     config.RoleNode,
		Root:     t.TempDir(),
		Services: map[service.Name]any{wireguard.Name: wireguard.DefaultServerConfig()},
		Tag:      NewInstanceTag(t, config.RoleNode),
	}
	require.NoError(t, os.MkdirAll(filepath.Join(loaded.Dir, "keys"), 0o700))
	require.NoError(t, os.MkdirAll(loaded.ServiceDir(wireguard.Name), 0o700))
	keyPath, err := loaded.KeyPath()
	require.NoError(t, err)
	require.NoError(t, key.Save(keyPath, operator, ""))
	seedConfigAccount(t, fakeRPC, programID, dvpn.Config{
		ChannelCloseGraceSeconds: 300,
		LeaseCloseGraceSeconds:   300,
		Mint:                     mint,
		NodeListingTTLSeconds:    86400,
	})

	id, err := program.ParseID(cfg.ID)
	require.NoError(t, err)
	address, _, err := dvpn.FindNodeAddress(programID, owner.PublicKey(), id)
	require.NoError(t, err)
	fakeRPC.SetNode(programID, address, dvpn.Node{
		ChannelPricePerGB:   5_000,
		ChannelPricePerHour: 200_000,
		LeasePricePerHour:   150_000,
		NodeID:              id,
		OpenedAt:            time.Now().Unix(),
		Owner:               owner.PublicKey(),
		Status:              dvpn.ListingStatusListed,
	})
	seedGrantAccount(t, fakeRPC, programID, owner.PublicKey(), operator.PublicKey(), program.PermsNode)

	db, err := store.Open(filepath.Join(loaded.Dir, "data.db"))
	require.NoError(t, err)

	return Home{
		DaemonKey:     operator,
		DB:            db,
		Loaded:        loaded,
		NodeAddress:   address,
		OwnerAddress:  owner.PublicKey(),
		OwnerKey:      owner,
		ProgramClient: program.NewClient(fakeRPC.NewClient(), programID, mint, loaded.File.RPC.Commitment),
		RPC:           fakeRPC,
		Sender:        tx.NewSender(fakeRPC.NewClient(), nil, tx.DefaultConfig(), zerolog.Nop()),
	}
}

func NewProviderHome(t *testing.T, fakeRPC *sdktest.FakeRPC) Home {
	t.Helper()

	programID := solana.MustPublicKeyFromBase58(programIDFixture)
	mint := solana.MustPublicKeyFromBase58(mintFixture)
	sdktest.PinBuildIdentity(t, programID, mint)
	dir := t.TempDir()
	operator := newPrivateKey(t)
	owner := newPrivateKey(t)
	cfg := config.DefaultProvider()
	cfg.ID = strings.Repeat("b", 64)
	cfg.Owner = owner.PublicKey().String()
	cfg.API.Hosts = []string{"203.0.113.1"}
	port, err := service.FreePort()
	require.NoError(t, err)

	cfg.API.Port = port
	loaded := &config.Loaded{
		Dir: dir,
		File: config.File{
			Peer: cli.PeerConfig{AllowPrivate: true},
			RPC:  fakeRPC.Config(),
			Tx:   tx.DefaultConfig(),
		},
		Provider: &cfg,
		Role:     config.RoleProvider,
		Tag:      NewInstanceTag(t, config.RoleProvider),
	}
	require.NoError(t, os.MkdirAll(filepath.Join(loaded.Dir, "keys"), 0o700))
	keyPath, err := loaded.KeyPath()
	require.NoError(t, err)
	require.NoError(t, key.Save(keyPath, operator, ""))
	seedConfigAccount(t, fakeRPC, programID, dvpn.Config{
		LeaseCloseGraceSeconds: 300,
		Mint:                   mint,
	})
	nodeAddress, server, nodeService := startNodeAPI(t, fakeRPC, wireguard.Name)
	nodeService.SetSessionSigner(operator.PublicKey())
	seedGrantAccount(t, fakeRPC, programID, owner.PublicKey(), operator.PublicKey(), program.PermsProvider)

	id, err := program.ParseID(cfg.ID)
	require.NoError(t, err)
	address, _, err := dvpn.FindProviderAddress(programID, owner.PublicKey(), id)
	require.NoError(t, err)

	uri, uriLen := packURI("https://provider.example.com")
	seedProviderAccount(t, fakeRPC, address, dvpn.Provider{
		Owner:      owner.PublicKey(),
		ProviderID: id,
		Status:     dvpn.ListingStatusListed,
		URI:        uri,
		URILen:     uriLen,
	})

	db, err := store.Open(filepath.Join(loaded.Dir, "data.db"))
	require.NoError(t, err)

	return Home{
		DaemonKey:     operator,
		DB:            db,
		Loaded:        loaded,
		NodeAddress:   nodeAddress,
		NodeServer:    server,
		NodeService:   nodeService,
		OwnerAddress:  owner.PublicKey(),
		OwnerKey:      owner,
		ProgramClient: program.NewClient(fakeRPC.NewClient(), programID, mint, loaded.File.RPC.Commitment),
		RPC:           fakeRPC,
		Sender:        tx.NewSender(fakeRPC.NewClient(), nil, tx.DefaultConfig(), zerolog.Nop()),
	}
}

func seedProviderAccount(t *testing.T, fakeRPC *sdktest.FakeRPC, address solana.PublicKey, provider dvpn.Provider) {
	t.Helper()
	fakeRPC.SetAccount(address, sdktest.Account{
		Data:  sdktest.EncodeAccount(dvpn.ProviderAccountDiscriminator, provider),
		Owner: solana.MustPublicKeyFromBase58(programIDFixture),
	})
}
