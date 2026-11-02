package tests

import (
	"errors"
	"testing"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"

	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/sentinel-official/sentinel-programs/clients/go/core/coretest"
	"github.com/sentinel-official/sentinel-programs/clients/go/distributor"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestFetchClaimBitmapBySeeds(t *testing.T) {
	t.Parallel()

	server := coretest.New(t)
	programID := solana.PublicKey{9}
	address, _, err := solana.FindProgramAddress([][]byte{[]byte("claim_bitmap"), {0x34, 0x12}}, programID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := (distributor.ClaimBitmap{ShardID: 0x1234, Bits: []byte{1}}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	data := append(distributor.ClaimBitmapAccountDiscriminator[:], encoded...)
	server.SetAccount(address, coretest.Account{Data: data, Owner: programID})

	got, err := distributor.FetchClaimBitmapBySeeds(t.Context(), server.Client(), programID, core.Options{}, 0x1234)
	if err != nil {
		t.Fatal(err)
	}
	if got.Address != address || got.Value.ShardID != 0x1234 {
		t.Fatalf("claim bitmap = %+v, want address %s", got, address)
	}
}

func TestFetchConfigBySeeds(t *testing.T) {
	t.Parallel()

	server := coretest.New(t)
	server.SetSlot(42)
	client := dvpn.NewClient(server.Client(), dvpn.ProgramID, rpc.CommitmentConfirmed)
	config := solana.MustPublicKeyFromBase58("5CwcUcMgZT4XCHLnzGiDtcYJPBbmqJQRPQRSZytSfTJA")
	data := make([]byte, dvpn.ConfigAccountSizeBytes)
	copy(data, dvpn.ConfigAccountDiscriminator[:])
	server.SetAccount(config, coretest.Account{Data: data, Owner: dvpn.ProgramID})

	got, err := client.FetchConfigBySeeds(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.Address != config || got.Slot != 42 {
		t.Fatalf("config = %+v", got)
	}

	channel := solana.MustPublicKeyFromBase58("5nSruYbYYLzadYF7AksMLm9yGo24U5ZVrmW9j1ghG38Y")
	owner := solana.MustPublicKeyFromBase58("11111111111111111111111111111112")
	id := [32]uint8{1}
	data = make([]byte, dvpn.ChannelAccountSizeBytes)
	copy(data, dvpn.ChannelAccountDiscriminator[:])
	data[8] = 1
	server.SetAccount(channel, coretest.Account{Data: data, Owner: dvpn.ProgramID})

	ch, err := client.FetchChannelBySeeds(t.Context(), owner, id)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Address != channel || ch.Value.ChannelID != id || ch.Slot != 42 {
		t.Fatalf("channel = %+v", ch)
	}

	client.ProgramID = solana.PublicKey{9}
	if _, err := client.FetchConfigBySeeds(t.Context()); !errors.Is(err, core.ErrAccountNotFound) {
		t.Fatalf("custom program ID = %v", err)
	}
}

func TestFetchVestingStateBySeeds(t *testing.T) {
	t.Parallel()

	server := coretest.New(t)
	programID := solana.PublicKey{9}
	address, _, err := solana.FindProgramAddress(
		[][]byte{[]byte("vesting_state"), {0x34, 0x12}, {0x78, 0x56, 0x34, 0x12}}, programID,
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := (distributor.VestingState{}).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	data := append(distributor.VestingStateAccountDiscriminator[:], encoded...)
	server.SetAccount(address, coretest.Account{Data: data, Owner: programID})
	client := distributor.NewClient(server.Client(), programID, rpc.CommitmentConfirmed)

	got, err := client.FetchVestingStateBySeeds(t.Context(), 0x1234, 0x12345678)
	if err != nil {
		t.Fatal(err)
	}
	if got.Address != address {
		t.Fatalf("address = %s, want %s", got.Address, address)
	}
}
