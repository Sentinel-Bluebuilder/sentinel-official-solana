//go:build integration

package treasury_test

import (
	"crypto/sha256"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/app"
	"github.com/sentinel-official/sentinel-snapshot/internal/auth"
	"github.com/sentinel-official/sentinel-snapshot/internal/bank"
	"github.com/sentinel-official/sentinel-snapshot/internal/ibcescrow"
	"github.com/sentinel-official/sentinel-snapshot/internal/staking"
	"github.com/sentinel-official/sentinel-snapshot/internal/treasury"
	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
)

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

func seedModuleBalance(
	t *testing.T,
	ctx sdk.Context,
	application *sentinelapp.App,
	moduleName string,
	amount math.Int,
) {
	t.Helper()

	coins := sdk.NewCoins(sdk.NewCoin(bank.DenomUDVPN, amount))
	if err := application.BankKeeper.MintCoins(ctx, minttypes.ModuleName, coins); err != nil {
		t.Fatalf("MintCoins: %v", err)
	}

	if err := application.BankKeeper.SendCoinsFromModuleToModule(
		ctx,
		minttypes.ModuleName,
		moduleName,
		coins,
	); err != nil {
		t.Fatalf("SendCoinsFromModuleToModule: %v", err)
	}
}

func newGatherInput(t *testing.T, h *app.Handle, escrows []ibcescrow.Channel) treasury.GatherInput {
	t.Helper()

	escrowAddresses := make(map[string]bool, len(escrows))
	for _, escrow := range escrows {
		escrowAddresses[escrow.Address.String()] = true
	}

	classified, err := auth.ClassifyAccounts(t.Context(), h, escrowAddresses)
	if err != nil {
		t.Fatalf("ClassifyAccounts: %v", err)
	}

	return treasury.GatherInput{
		Credited:                 treasury.CreditedTotals{Rewards: math.ZeroInt(), Commission: math.ZeroInt()},
		PermanentLockedPrincipal: math.ZeroInt(),
		NonLeafAttributedEscrow:  math.ZeroInt(),
		Escrows:                  escrows,
		ClassifiedAccounts:       classified,
		BalanceByAddress:         bank.BalancesByAddress(h),
		DelegatedByAddress:       staking.DelegatedByAddress(h),
		UnbondingByAddress:       staking.UnbondingByAddress(h),
	}
}

func sha256Sum20(s string) []byte {
	sum := sha256.Sum256([]byte(s))

	return sum[:20]
}
