package strandcheck_test

import (
	"strings"
	"testing"

	"github.com/sentinel-official/sentinel-snapshot/internal/ledger"
	"github.com/sentinel-official/sentinel-snapshot/internal/strandcheck"
)

func TestValidateNoStrandedUnrevealed_Escrow(t *testing.T) {
	t.Parallel()

	stranded := repeatedAddressBytes(9)
	records := []ledger.Record{
		{Address: stranded, AddressBech32: "sent1escrow", PubKey: nil, LiquidUDVPN: 4200},
	}
	familyByAddress := map[[20]byte]string{stranded: "escrow"}

	err := strandcheck.ValidateNoStrandedUnrevealed(records, familyByAddress)
	if err == nil {
		t.Fatal("expected the gate to fail on a stranded escrow sentinel")
	}

	if !strings.Contains(err.Error(), "escrow") || !strings.Contains(err.Error(), "sent1escrow") {
		t.Fatalf("error must name the family and address: %v", err)
	}
}
