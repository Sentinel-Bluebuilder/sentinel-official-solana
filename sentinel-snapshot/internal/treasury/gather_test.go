//go:build integration

package treasury_test

import (
	"strings"
	"testing"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	ibcfeetypes "github.com/cosmos/ibc-go/v7/modules/apps/29-fee/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/apptest"
	"github.com/sentinel-official/sentinel-snapshot/internal/bank"
	"github.com/sentinel-official/sentinel-snapshot/internal/staking"
	"github.com/sentinel-official/sentinel-snapshot/internal/treasury"
	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
)

//nolint:paralleltest
func TestGather_ComponentWise(t *testing.T) {
	treasuryAddress := sdk.AccAddress(sha256Sum20("sentinel-snapshot/internal/treasury.treasuryAccount"))
	dustOperator := sdk.ValAddress(sha256Sum20("sentinel-snapshot/internal/treasury.dustValidator"))

	wantFeeCollectorSweep := math.NewInt(250)
	wantTreasuryBalance := math.NewInt(1_000)
	wantPermanentLocked := math.NewInt(999)

	validatorTokens := math.NewInt(10)
	validatorShares := math.LegacyNewDec(3)
	delegatedShares := math.LegacyNewDec(1)
	wantTreasuryDelegated := staking.TokensFromShares(delegatedShares, validatorTokens, validatorShares)
	wantTreasuryUnbonding := math.NewInt(5)
	wantDustValStakingDust := validatorTokens.Sub(wantTreasuryDelegated)

	h := apptest.SeedHandle(t, func(ctx sdk.Context, application *sentinelapp.App) {
		params := application.StakingKeeper.GetParams(ctx)

		params.BondDenom = bank.DenomUDVPN
		if err := application.StakingKeeper.SetParams(ctx, params); err != nil {
			t.Fatalf("SetParams: %v", err)
		}

		seedModuleBalance(t, ctx, application, authtypes.FeeCollectorName, wantFeeCollectorSweep)

		treasuryAccount := application.AccountKeeper.NewAccountWithAddress(ctx, treasuryAddress)
		if err := treasuryAccount.SetPubKey(ed25519.GenPrivKey().PubKey()); err != nil {
			t.Fatalf("SetPubKey: %v", err)
		}

		application.AccountKeeper.SetAccount(ctx, treasuryAccount)
		seedBalance(t, ctx, application, treasuryAddress, wantTreasuryBalance)

		validator, err := stakingtypes.NewValidator(dustOperator, ed25519.GenPrivKey().PubKey(), stakingtypes.Description{})
		if err != nil {
			t.Fatalf("NewValidator: %v", err)
		}

		validator.Tokens = validatorTokens
		validator.DelegatorShares = validatorShares
		application.StakingKeeper.SetValidator(ctx, validator)

		delegation := stakingtypes.NewDelegation(treasuryAddress, dustOperator, delegatedShares)
		application.StakingKeeper.SetDelegation(ctx, delegation)

		unbonding := stakingtypes.NewUnbondingDelegation(
			treasuryAddress,
			dustOperator,
			ctx.BlockHeight(),
			ctx.BlockTime(),
			wantTreasuryUnbonding,
			1,
		)
		application.StakingKeeper.SetUnbondingDelegation(ctx, unbonding)
	})

	input := newGatherInput(t, h, nil)
	input.PermanentLockedPrincipal = wantPermanentLocked

	got, err := treasury.Gather(t.Context(), h, input)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	wantDirectlyMeasured := wantTreasuryBalance.Add(wantTreasuryDelegated).Add(wantTreasuryUnbonding)
	if !got.DirectlyMeasured.Equal(wantDirectlyMeasured) {
		t.Fatalf("DirectlyMeasured = %s, want %s", got.DirectlyMeasured, wantDirectlyMeasured)
	}

	if !got.IBCFeeEscrow.IsZero() {
		t.Fatalf("IBCFeeEscrow = %s, want 0 (confirmed-empty)", got.IBCFeeEscrow)
	}

	if !got.NonLeafAttributedEscrow.IsZero() {
		t.Fatalf("NonLeafAttributedEscrow = %s, want 0 (none seeded)", got.NonLeafAttributedEscrow)
	}

	if !got.DistributionSweep.Equal(wantFeeCollectorSweep) {
		t.Fatalf("DistributionSweep = %s, want %s (the fee_collector amount, auto-swept by the "+
			"harness's second-block BeginBlocker)", got.DistributionSweep, wantFeeCollectorSweep)
	}

	if !got.StakingDust.Equal(wantDustValStakingDust) {
		t.Fatalf("StakingDust = %s, want %s (harness genesis validator contributes exactly 0 "+
			"dust: shares==tokens 1:1)", got.StakingDust, wantDustValStakingDust)
	}

	if !got.PermanentLockedPrincipal.Equal(wantPermanentLocked) {
		t.Fatalf("PermanentLockedPrincipal = %s, want %s", got.PermanentLockedPrincipal, wantPermanentLocked)
	}

	wantTotal := wantDirectlyMeasured.Add(wantFeeCollectorSweep).Add(wantDustValStakingDust).Add(wantPermanentLocked)
	if !got.Total().Equal(wantTotal) {
		t.Fatalf("Components.Total() = %s, want %s", got.Total(), wantTotal)
	}
}

//nolint:paralleltest
func TestGather_RejectsNonzeroIBCFeeEscrow(t *testing.T) {
	wantIBCFeeEscrow := math.NewInt(42)

	h := apptest.SeedHandle(t, func(ctx sdk.Context, application *sentinelapp.App) {
		seedModuleBalance(t, ctx, application, ibcfeetypes.ModuleName, wantIBCFeeEscrow)
	})

	_, err := treasury.Gather(t.Context(), h, newGatherInput(t, h, nil))
	if err == nil {
		t.Fatal("Gather should have failed loud on a nonzero ibcfee module balance, not swept it silently")
	}

	if !strings.Contains(err.Error(), wantIBCFeeEscrow.String()) {
		t.Fatalf("Gather error %q does not name the ibc fee escrow amount %s", err.Error(), wantIBCFeeEscrow)
	}

	if !strings.Contains(err.Error(), "ibcfee") {
		t.Fatalf("Gather error %q does not name the ibcfee module", err.Error())
	}
}
