//go:build integration

package distribution_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/apptest"
	"github.com/sentinel-official/sentinel-snapshot/internal/bank"
	"github.com/sentinel-official/sentinel-snapshot/internal/distribution"
	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
)

//nolint:paralleltest
func TestCreditedByAddress(t *testing.T) {
	const (
		selfStake  = 500_000
		otherStake = 300_000
		allocated  = 800
	)

	wantOutstanding := math.LegacyNewDec(allocated)
	wantCommission := math.LegacyNewDec(80)
	wantSelfCredit := math.NewInt(450)
	wantOtherCredit := math.NewInt(270)
	wantCommissionCredited := math.NewInt(80)

	operator := sdk.ValAddress(sha256Sum20("sentinel-snapshot/internal/distribution.testValidator"))
	operatorAddress := sdk.AccAddress(operator)
	delegatorAddress := apptest.Address(t)

	h := apptest.SeedHandle(t, func(ctx sdk.Context, application *sentinelapp.App) {
		stakeDenom := application.StakingKeeper.BondDenom(ctx)

		seedBalance := func(address sdk.AccAddress, amount int64) {
			coins := sdk.NewCoins(sdk.NewCoin(stakeDenom, math.NewInt(amount)))
			if err := application.BankKeeper.MintCoins(ctx, minttypes.ModuleName, coins); err != nil {
				t.Fatalf("MintCoins: %v", err)
			}

			err := application.BankKeeper.SendCoinsFromModuleToAccount(ctx, minttypes.ModuleName, address, coins)
			if err != nil {
				t.Fatalf("SendCoinsFromModuleToAccount: %v", err)
			}
		}
		seedBalance(operatorAddress, selfStake)
		seedBalance(delegatorAddress, otherStake)

		msgServer := stakingkeeper.NewMsgServerImpl(application.StakingKeeper)
		goCtx := sdk.WrapSDKContext(ctx)

		createMsg, err := stakingtypes.NewMsgCreateValidator(
			operator, ed25519.GenPrivKey().PubKey(),
			sdk.NewCoin(stakeDenom, math.NewInt(selfStake)),
			stakingtypes.Description{Moniker: "test-validator"},
			stakingtypes.NewCommissionRates(
				math.LegacyNewDecWithPrec(10, 2),
				math.LegacyNewDecWithPrec(20, 2),
				math.LegacyNewDecWithPrec(1, 2),
			),
			math.OneInt(),
		)
		if err != nil {
			t.Fatalf("NewMsgCreateValidator: %v", err)
		}

		if _, err := msgServer.CreateValidator(goCtx, createMsg); err != nil {
			t.Fatalf("CreateValidator: %v", err)
		}

		delegateMsg := stakingtypes.NewMsgDelegate(
			delegatorAddress, operator, sdk.NewCoin(stakeDenom, math.NewInt(otherStake)),
		)
		if _, err := msgServer.Delegate(goCtx, delegateMsg); err != nil {
			t.Fatalf("Delegate: %v", err)
		}

		validator, found := application.StakingKeeper.GetValidator(ctx, operator)
		if !found {
			t.Fatalf("validator %s not found after CreateValidator", operator)
		}

		if !validator.Tokens.Equal(math.NewInt(selfStake + otherStake)) {
			t.Fatalf(
				"validator tokens = %s, want %d (sanity check before allocating rewards)",
				validator.Tokens,
				selfStake+otherStake,
			)
		}

		application.DistributionKeeper.AllocateTokensToValidator(
			ctx, validator, sdk.NewDecCoins(sdk.NewDecCoin(bank.DenomUDVPN, math.NewInt(allocated))),
		)
	})

	if got := distribution.ValidatorOutstandingRewards(h, operator); !got.Equal(wantOutstanding) {
		t.Fatalf("ValidatorOutstandingRewards = %s, want %s", got, wantOutstanding)
	}

	if got := distribution.ValidatorCommission(h, operator); !got.Equal(wantCommission) {
		t.Fatalf("ValidatorCommission = %s, want %s", got, wantCommission)
	}

	credits, err := distribution.DelegatorRewards(h, operator)
	if err != nil {
		t.Fatalf("DelegatorRewards: %v", err)
	}

	creditByAddress := make(map[string]distribution.Credit, len(credits))
	for _, credit := range credits {
		creditByAddress[credit.Address.String()] = credit
	}

	selfCredit, ok := creditByAddress[operatorAddress.String()]
	if !ok {
		t.Fatalf("no DelegatorRewards credit for self-delegator %s", operatorAddress)
	}

	if !selfCredit.Amount.Equal(math.LegacyNewDec(450)) {
		t.Fatalf("self-delegator raw reward = %s, want 450", selfCredit.Amount)
	}

	otherCredit, ok := creditByAddress[delegatorAddress.String()]
	if !ok {
		t.Fatalf("no DelegatorRewards credit for delegator %s", delegatorAddress)
	}

	if !otherCredit.Amount.Equal(math.LegacyNewDec(270)) {
		t.Fatalf("delegator raw reward = %s, want 270", otherCredit.Amount)
	}

	credited, err := distribution.CreditedByAddress(h)
	if err != nil {
		t.Fatalf("CreditedByAddress: %v", err)
	}

	if got := credited.Rewards[operatorAddress.String()]; !got.Equal(wantSelfCredit) {
		t.Fatalf("CreditedByAddress rewards[self] = %s, want %s", got, wantSelfCredit)
	}

	if got := credited.Rewards[delegatorAddress.String()]; !got.Equal(wantOtherCredit) {
		t.Fatalf("CreditedByAddress rewards[other] = %s, want %s", got, wantOtherCredit)
	}

	if got := credited.Commission[operatorAddress.String()]; !got.Equal(wantCommissionCredited) {
		t.Fatalf("CreditedByAddress commission[operator] = %s, want %s", got, wantCommissionCredited)
	}
}
