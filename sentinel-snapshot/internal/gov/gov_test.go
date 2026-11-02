//go:build integration

package gov_test

import (
	"crypto/sha256"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/apptest"
	"github.com/sentinel-official/sentinel-snapshot/internal/bank"
	"github.com/sentinel-official/sentinel-snapshot/internal/gov"
	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
)

//nolint:paralleltest
func TestDepositByAddress(t *testing.T) {
	depositorAddress := sdk.AccAddress(sha256Sum20("sentinel-snapshot/internal/gov.depositor"))
	wantDeposit := math.NewInt(6_789)

	h := apptest.SeedHandle(t, func(ctx sdk.Context, application *sentinelapp.App) {
		seedBalance(t, ctx, application, depositorAddress, wantDeposit)

		proposal, err := application.GovKeeper.SubmitProposal(ctx, nil, "", "test title", "test summary", depositorAddress)
		if err != nil {
			t.Fatalf("SubmitProposal: %v", err)
		}

		if _, err := application.GovKeeper.AddDeposit(
			ctx,
			proposal.Id,
			depositorAddress,
			sdk.NewCoins(sdk.NewCoin(bank.DenomUDVPN, wantDeposit)),
		); err != nil {
			t.Fatalf("AddDeposit (gov): %v", err)
		}
	})

	deposits := gov.DepositByAddress(h)
	if got := deposits[depositorAddress.String()]; !got.Equal(wantDeposit) {
		t.Fatalf("DepositByAddress[%s] = %s, want %s", depositorAddress, got, wantDeposit)
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

func sha256Sum20(s string) []byte {
	sum := sha256.Sum256([]byte(s))

	return sum[:20]
}
