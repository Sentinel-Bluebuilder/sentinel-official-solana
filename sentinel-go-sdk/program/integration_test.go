//go:build integration

package program_test

import (
	"testing"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
	"github.com/sentinel-official/sentinel-go-sdk/v3/wireguard"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestClient_OpenNodeAndDiscover(t *testing.T) {
	t.Parallel()

	localnet := sdktest.StartLocalnet(t)
	ctx := t.Context()

	owner, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	require.NoError(t, localnet.FundSOL(ctx, owner.PublicKey(), 5))
	require.NoError(t, localnet.FundTokens(ctx, owner.PublicKey(), 10_000_000_000))

	programClient := program.NewClient(localnet.RPC, localnet.ProgramID, localnet.Mint, rpc.CommitmentConfirmed)

	ownerToken, _, err := solana.FindAssociatedTokenAddressWithProgram(
		owner.PublicKey(),
		localnet.Mint,
		solana.Token2022ProgramID,
	)
	require.NoError(t, err)

	vault, err := programClient.VaultAddress()
	require.NoError(t, err)

	var id [32]uint8

	id[0] = 7

	prices := dvpn.NodePrices{
		ChannelPricePerGB:   1_000,
		ChannelPricePerHour: 2_000,
		LeasePricePerHour:   3_000,
	}
	openParams := dvpn.OpenNodeParams{
		Bond:              1_000_000_000,
		Country:           [2]uint8{'D', 'E'},
		Mint:              localnet.Mint,
		NodeID:            id,
		NodeRentRecipient: owner.PublicKey(),
		Owner:             owner.PublicKey(),
		OwnerToken:        ownerToken,
		Prices:            prices,
		Protocols:         wireguard.Name.Bit(),
		RentPayer:         owner.PublicKey(),
		Signer:            owner.PublicKey(),
		URI:               []byte("https://example.invalid:12345"),
		Vault:             vault,
	}

	openIx, err := dvpn.BuildOpenNodeInstruction(localnet.ProgramID, openParams)
	require.NoError(t, err)

	openIxs := []solana.Instruction{openIx}

	receipt, err := tx.Send(ctx, localnet.RPC, localnet.WS, owner, tx.DefaultConfig(), openIxs)
	require.NoError(t, err)
	require.NotZero(t, receipt.Slot)

	address, _, err := dvpn.FindNodeAddress(localnet.ProgramID, owner.PublicKey(), id)
	require.NoError(t, err)

	node, err := dvpn.FetchNode(ctx, programClient.RPC, address, programClient.Options())
	require.NoError(t, err)
	require.Equal(t, owner.PublicKey(), node.Value.Owner)

	cfg, err := programClient.Config(ctx)
	require.NoError(t, err)

	before, err := programClient.FindNodeAccounts(ctx, dvpn.NodeStatusFilter(dvpn.ListingStatusListed))
	require.NoError(t, err)
	require.Empty(t, before)

	listParams := dvpn.ListNodeParams{
		Node:   address,
		Signer: owner.PublicKey(),
	}

	listIx, err := dvpn.BuildListNodeInstruction(localnet.ProgramID, listParams)
	require.NoError(t, err)

	listIxs := []solana.Instruction{listIx}

	_, err = tx.Send(ctx, localnet.RPC, localnet.WS, owner, tx.DefaultConfig(), listIxs)
	require.NoError(t, err)

	after, err := programClient.FindNodeAccounts(ctx, dvpn.NodeStatusFilter(dvpn.ListingStatusListed))
	require.NoError(t, err)
	require.Len(t, after, 1)
	require.Equal(t, address, after[0].Address)
	require.True(t, program.Live(after[0].Value, cfg, time.Now().Unix()))
}
