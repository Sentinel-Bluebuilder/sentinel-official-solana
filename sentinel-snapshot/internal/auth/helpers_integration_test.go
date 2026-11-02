//go:build integration

package auth_test

import (
	"crypto/sha256"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/auth"
	"github.com/sentinel-official/sentinel-snapshot/internal/bank"
	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
)

func newAddress(label string) sdk.AccAddress {
	sum := sha256.Sum256([]byte(label))

	return sdk.AccAddress(sum[:20])
}

func classOf(t *testing.T, accounts []auth.Account, target sdk.AccAddress) auth.Class {
	t.Helper()

	for _, account := range accounts {
		if account.AddressBech32 == target.String() {
			return account.Class
		}
	}

	t.Fatalf("address %s not found in walk", target)

	return 0
}

func seedBalance(t *testing.T, ctx sdk.Context, application *sentinelapp.App, address sdk.AccAddress, amount math.Int) {
	t.Helper()

	coins := sdk.NewCoins(sdk.NewCoin(bank.DenomUDVPN, amount))
	if err := application.BankKeeper.MintCoins(ctx, minttypes.ModuleName, coins); err != nil {
		t.Fatal(err)
	}

	if err := application.BankKeeper.SendCoinsFromModuleToAccount(ctx, minttypes.ModuleName, address, coins); err != nil {
		t.Fatal(err)
	}
}
