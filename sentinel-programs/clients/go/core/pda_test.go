package core_test

import (
	"testing"

	"github.com/solana-foundation/solana-go/v2"

	"github.com/sentinel-official/sentinel-programs/clients/go/core"
)

func TestFindProgramDataAddress(t *testing.T) {
	t.Parallel()

	programID := solana.MustPublicKeyFromBase58("SentineLDVPN1111111111111111111111111111111")
	address, err := core.FindProgramDataAddress(programID)
	if err != nil {
		t.Fatal(err)
	}
	if address.String() != "5GSKPPg13FFSanypyix8HbwRkzbAErWUGKuVnNjTsw1C" {
		t.Fatalf("program data = %s", address)
	}
}
