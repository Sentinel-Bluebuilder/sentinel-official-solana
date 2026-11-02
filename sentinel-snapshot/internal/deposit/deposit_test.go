//go:build integration

package deposit_test

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/apptest"
	"github.com/sentinel-official/sentinel-snapshot/internal/bank"
	"github.com/sentinel-official/sentinel-snapshot/internal/deposit"
	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
)

//nolint:paralleltest
func TestByAddress(t *testing.T) {
	depositorAddress := apptest.Address(t)
	wantDeposit := math.NewInt(12_345)

	h := apptest.SeedHandle(t, func(ctx sdk.Context, application *sentinelapp.App) {
		seedBalance(t, ctx, application, depositorAddress, wantDeposit)

		if err := application.VPNKeeper.Deposit.AddDeposit(
			ctx,
			depositorAddress,
			sdk.NewCoins(sdk.NewCoin(bank.DenomUDVPN, wantDeposit)),
		); err != nil {
			t.Fatalf("AddDeposit: %v", err)
		}
	})

	deposits := deposit.ByAddress(h)
	if got := deposits[depositorAddress.String()]; !got.Equal(wantDeposit) {
		t.Fatalf("ByAddress[%s] = %s, want %s", depositorAddress, got, wantDeposit)
	}
}

func seedBalance(t *testing.T, ctx sdk.Context, application *sentinelapp.App, address sdk.AccAddress, amount math.Int) {
	t.Helper()

	coins := sdk.NewCoins(sdk.NewCoin(bank.DenomUDVPN, amount))
	if err := application.BankKeeper.MintCoins(ctx, minttypes.ModuleName, coins); err != nil {
		t.Fatalf("MintCoins: %v", err)
	}

	if err := application.BankKeeper.SendCoinsFromModuleToAccount(ctx, minttypes.ModuleName, address, coins); err != nil {
		t.Fatalf("SendCoinsFromModuleToAccount: %v", err)
	}
}
