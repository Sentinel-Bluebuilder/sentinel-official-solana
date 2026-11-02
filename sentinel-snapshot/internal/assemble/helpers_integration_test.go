//go:build integration

package assemble_test

import (
	"crypto/sha256"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/bank"
	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
)

func newAddress(label string) sdk.AccAddress {
	return sdk.AccAddress(sha256Sum20(label))
}

//nolint:unparam
func seedBalance(
	t *testing.T,
	ctx sdk.Context,
	application *sentinelapp.App,
	address sdk.AccAddress,
	denom string,
	amount math.Int,
) {
	t.Helper()

	coins := sdk.NewCoins(sdk.NewCoin(denom, amount))
	if err := application.BankKeeper.MintCoins(ctx, minttypes.ModuleName, coins); err != nil {
		t.Fatalf("MintCoins: %v", err)
	}

	if err := application.BankKeeper.SendCoinsFromModuleToAccount(ctx, minttypes.ModuleName, address, coins); err != nil {
		t.Fatalf("SendCoinsFromModuleToAccount: %v", err)
	}
}

func seedAccount(t *testing.T, ctx sdk.Context, application *sentinelapp.App, address sdk.AccAddress, amount uint64) {
	t.Helper()

	account := application.AccountKeeper.NewAccountWithAddress(ctx, address)
	application.AccountKeeper.SetAccount(ctx, account)
	seedBalance(t, ctx, application, address, bank.DenomUDVPN, math.NewIntFromUint64(amount))
}

func sha256Sum20(s string) []byte {
	sum := sha256.Sum256([]byte(s))

	return sum[:20]
}
