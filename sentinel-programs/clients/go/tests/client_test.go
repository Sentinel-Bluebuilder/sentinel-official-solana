package tests

import (
	"errors"
	"testing"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"

	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/sentinel-official/sentinel-programs/clients/go/core/coretest"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestClient_FetchNode(t *testing.T) {
	t.Parallel()

	server := coretest.New(t)
	programID := solana.PublicKey{9}
	address := solana.PublicKey{2}
	server.SetSlot(42)
	data := make([]byte, dvpn.NodeAccountSizeBytes)
	copy(data, dvpn.NodeAccountDiscriminator[:])
	data[8] = 7
	server.SetAccount(address, coretest.Account{Data: data, Owner: programID})
	client := dvpn.NewClient(server.Client(), programID, rpc.CommitmentConfirmed)

	got, err := client.FetchNode(t.Context(), address)
	if err != nil {
		t.Fatal(err)
	}
	if got.Address != address || got.Slot != 42 || got.Value.NodeID[0] != 7 {
		t.Fatalf("node = %+v", got)
	}
	if client.Options().Owner != programID || client.Options().Commitment != rpc.CommitmentConfirmed {
		t.Fatalf("options = %+v", client.Options())
	}

	server.SetAccount(address, coretest.Account{Data: data, Owner: solana.PublicKey{3}})
	if _, err := client.FetchNode(t.Context(), address); !errors.Is(err, core.ErrOwnerMismatch) {
		t.Fatalf("foreign owner = %v", err)
	}
	if _, err := client.FetchNode(t.Context(), solana.PublicKey{4}); !errors.Is(err, core.ErrAccountNotFound) {
		t.Fatalf("missing node = %v", err)
	}
}
