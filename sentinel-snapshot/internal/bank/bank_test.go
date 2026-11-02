//go:build integration

package bank_test

import (
	"testing"

	"cosmossdk.io/math"

	"github.com/sentinel-official/sentinel-snapshot/internal/apptest"
	"github.com/sentinel-official/sentinel-snapshot/internal/bank"
)

//nolint:paralleltest
func TestBalanceUDVPN(t *testing.T) {
	address, want := apptest.Address(t), math.NewInt(1_000_000)

	h := apptest.SeedBalance(t, address, want)
	if got := bank.BalanceUDVPN(h, address); !got.Equal(want) {
		t.Fatalf("balance = %s, want %s", got, want)
	}
}

//nolint:paralleltest
func TestSupplyUDVPN(t *testing.T) {
	address, want := apptest.Address(t), math.NewInt(1_000_000)

	h := apptest.SeedBalance(t, address, want)
	if got := bank.SupplyUDVPN(h); got.LT(want) {
		t.Fatalf("supply = %s, want >= %s", got, want)
	}
}
