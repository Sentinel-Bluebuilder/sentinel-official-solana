package program_test

import (
	"testing"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"

	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

//nolint:gochecknoglobals
var testMint = solana.PublicKey{1}

func newClient(t *testing.T) (*program.Client, *sdktest.FakeRPC) {
	t.Helper()

	fakeRPC := sdktest.NewFakeRPC(t)
	programClient := program.NewClient(fakeRPC.NewClient(), dvpn.ProgramID, testMint, rpc.CommitmentConfirmed)

	return programClient, fakeRPC
}
