package tests

import (
	"testing"

	"github.com/solana-foundation/solana-go/v2"

	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestFindVaultAddress(t *testing.T) {
	t.Parallel()

	var mint solana.PublicKey
	for i := range mint {
		mint[i] = 4
	}
	config, _, err := dvpn.FindConfigAddress(dvpn.ProgramID)
	if err != nil {
		t.Fatal(err)
	}

	vault, _, err := dvpn.FindVaultAddress(config, solana.Token2022ProgramID, mint)
	if err != nil {
		t.Fatal(err)
	}
	if vault.String() != "9a8CS4TUPe959vs5wTZHFuB2cj9ZeCnxNw5wm9LVwrAC" {
		t.Fatalf("vault = %s", vault)
	}

	ix, err := dvpn.BuildInitializeConfigInstruction(dvpn.ProgramID, dvpn.InitializeConfigParams{Mint: mint})
	if err != nil {
		t.Fatal(err)
	}
	if got := ix.Accounts()[5].PublicKey; got != vault {
		t.Fatalf("resolved vault = %s, want %s", got, vault)
	}
}
