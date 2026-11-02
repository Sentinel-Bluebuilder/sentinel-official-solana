//go:build integration

package assemble_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	vestingtypes "github.com/cosmos/cosmos-sdk/x/auth/vesting/types"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/apptest"
	"github.com/sentinel-official/sentinel-snapshot/internal/assemble"
	"github.com/sentinel-official/sentinel-snapshot/internal/bank"
	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
)

//nolint:paralleltest
func TestRun_ExcludesEscrowFromLeaves(t *testing.T) {
	escrow := newAddress("escrow-in-assemble")
	h := apptest.SeedHandle(t, func(ctx sdk.Context, application *sentinelapp.App) {
		seedAccount(t, ctx, application, escrow, 1234)
	})

	escrowAddresses := map[string]bool{escrow.String(): true}

	result, err := assemble.Run(t.Context(), h, escrowAddresses, 1)
	if err != nil {
		t.Fatal(err)
	}

	for _, account := range result.Accounts {
		if account.AddressBech32 == escrow.String() {
			t.Fatalf("escrow address was leafed: %+v", account)
		}
	}
}

//nolint:paralleltest
func TestRun_TwoPassAccountWalk(t *testing.T) {
	liquidPrivKey := secp256k1.GenPrivKey()
	liquidAddress := sdk.AccAddress(liquidPrivKey.PubKey().Address())
	operator := sdk.ValAddress(liquidAddress)

	continuousAddress := sdk.AccAddress(sha256Sum20("sentinel-snapshot/internal/assemble.continuousVestingAccount"))
	permanentAddress := sdk.AccAddress(sha256Sum20("sentinel-snapshot/internal/assemble.permanentLockedAccount"))

	const (
		selfStake          = 100_000
		liquidExtraBalance = 500
		allocated          = 1_000
	)

	wantRewards := math.NewInt(900)
	wantCommission := math.NewInt(100)

	wantLiquidAccountLiquid := uint64(101_500)

	const continuousPrincipal = 1_000_000

	wantContinuousLiquid := uint64(500_000)
	wantContinuousLocked := uint64(500_000)

	const (
		permanentPrincipal    = 1_000
		permanentExtraBalance = 50
	)

	wantPermanentLiquid := uint64(50)

	h := apptest.SeedHandle(t, func(ctx sdk.Context, application *sentinelapp.App) {
		params := application.StakingKeeper.GetParams(ctx)

		params.BondDenom = bank.DenomUDVPN
		if err := application.StakingKeeper.SetParams(ctx, params); err != nil {
			t.Fatalf("SetParams: %v", err)
		}

		haltUnix := ctx.BlockTime().Unix()

		liquidBase := application.AccountKeeper.NewAccountWithAddress(ctx, liquidAddress).(*authtypes.BaseAccount)
		if err := liquidBase.SetPubKey(liquidPrivKey.PubKey()); err != nil {
			t.Fatalf("SetPubKey: %v", err)
		}

		application.AccountKeeper.SetAccount(ctx, liquidBase)
		seedBalance(t, ctx, application, liquidAddress, bank.DenomUDVPN, math.NewInt(selfStake+liquidExtraBalance))

		msgServer := stakingkeeper.NewMsgServerImpl(application.StakingKeeper)
		goCtx := sdk.WrapSDKContext(ctx)

		createMsg, err := stakingtypes.NewMsgCreateValidator(
			operator, ed25519.GenPrivKey().PubKey(),
			sdk.NewCoin(bank.DenomUDVPN, math.NewInt(selfStake)),
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

		validator, found := application.StakingKeeper.GetValidator(ctx, operator)
		if !found {
			t.Fatalf("validator %s not found after CreateValidator", operator)
		}

		application.DistributionKeeper.AllocateTokensToValidator(
			ctx, validator, sdk.NewDecCoins(sdk.NewDecCoin(bank.DenomUDVPN, math.NewInt(allocated))),
		)

		continuousBase := application.AccountKeeper.NewAccountWithAddress(ctx, continuousAddress).(*authtypes.BaseAccount)
		continuousVesting := vestingtypes.NewContinuousVestingAccount(
			continuousBase, sdk.NewCoins(sdk.NewCoin(bank.DenomUDVPN, math.NewInt(continuousPrincipal))),
			haltUnix-1000, haltUnix+1000,
		)
		application.AccountKeeper.SetAccount(ctx, continuousVesting)
		seedBalance(t, ctx, application, continuousAddress, bank.DenomUDVPN, math.NewInt(continuousPrincipal))

		permanentBase := application.AccountKeeper.NewAccountWithAddress(ctx, permanentAddress).(*authtypes.BaseAccount)
		permanentVesting := vestingtypes.NewPermanentLockedAccount(
			permanentBase, sdk.NewCoins(sdk.NewCoin(bank.DenomUDVPN, math.NewInt(permanentPrincipal))),
		)
		application.AccountKeeper.SetAccount(ctx, permanentVesting)
		seedBalance(
			t, ctx, application, permanentAddress, bank.DenomUDVPN,
			math.NewInt(permanentPrincipal+permanentExtraBalance),
		)
	})

	result, err := assemble.Run(t.Context(), h, nil, 1)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	accountByAddress := make(map[string]assemble.Account, len(result.Accounts))
	for _, account := range result.Accounts {
		accountByAddress[account.AddressBech32] = account
	}

	liquidAccount, ok := accountByAddress[liquidAddress.String()]
	if !ok {
		t.Fatalf("no leaf for liquid account %s (must not be pruned)", liquidAddress)
	}

	if liquidAccount.LiquidUDVPN != wantLiquidAccountLiquid ||
		liquidAccount.LockedUDVPN != 0 ||
		liquidAccount.Schedule != nil {
		t.Fatalf("liquid account = {LiquidUDVPN:%d LockedUDVPN:%d Schedule:%v}, "+
			"want {LiquidUDVPN:%d LockedUDVPN:0 Schedule:nil}",
			liquidAccount.LiquidUDVPN, liquidAccount.LockedUDVPN, liquidAccount.Schedule, wantLiquidAccountLiquid)
	}

	if len(liquidAccount.CompressedPubKey) != 33 {
		t.Fatalf(
			"liquid account CompressedPubKey len = %d, want 33 (revealed secp256k1)",
			len(liquidAccount.CompressedPubKey),
		)
	}

	continuousAccount, ok := accountByAddress[continuousAddress.String()]
	if !ok {
		t.Fatalf("no leaf for continuous vesting account %s (must not be pruned)", continuousAddress)
	}

	if continuousAccount.LiquidUDVPN != wantContinuousLiquid {
		t.Fatalf("continuous account Liquid = %d, want %d", continuousAccount.LiquidUDVPN, wantContinuousLiquid)
	}

	if continuousAccount.LockedUDVPN != wantContinuousLocked {
		t.Fatalf("continuous account Locked = %d, want %d", continuousAccount.LockedUDVPN, wantContinuousLocked)
	}

	if continuousAccount.Schedule == nil {
		t.Fatal("continuous account Schedule = nil, want a re-anchored schedule")
	} else {
		haltUnix := h.BlockTimeUnix()

		if continuousAccount.Schedule.Kind != 0 {
			t.Fatalf("continuous account Schedule.Kind = %d, want Continuous(0)", continuousAccount.Schedule.Kind)
		}

		if continuousAccount.Schedule.StartUnix != haltUnix {
			t.Fatalf(
				"continuous account Schedule.StartUnix = %d, want halt time = %d",
				continuousAccount.Schedule.StartUnix, haltUnix,
			)
		}

		if continuousAccount.Schedule.EndUnix != haltUnix+1000 {
			t.Fatalf("continuous account Schedule.EndUnix = %d, want %d (no shortfall: original "+
				"EndUnix preserved)", continuousAccount.Schedule.EndUnix, haltUnix+1000)
		}

		if continuousAccount.Schedule.TotalUDVPN != wantContinuousLocked {
			t.Fatalf("continuous account Schedule.TotalUDVPN = %d, want %d", continuousAccount.Schedule.TotalUDVPN, wantContinuousLocked)
		}
	}

	permanentAccount, ok := accountByAddress[permanentAddress.String()]
	if !ok {
		t.Fatalf("no leaf for permanent-locked account %s (must not be pruned)", permanentAddress)
	}

	if permanentAccount.LiquidUDVPN != wantPermanentLiquid ||
		permanentAccount.LockedUDVPN != 0 ||
		permanentAccount.Schedule != nil {
		t.Fatalf("permanent-locked account = {LiquidUDVPN:%d LockedUDVPN:%d Schedule:%v}, want {LiquidUDVPN:%d "+
			"LockedUDVPN:0 Schedule:nil} (leaf carries liquid only; principal swept)",
			permanentAccount.LiquidUDVPN, permanentAccount.LockedUDVPN, permanentAccount.Schedule, wantPermanentLiquid)
	}

	if !result.PermanentLockedPrincipal.Equal(math.NewInt(permanentPrincipal)) {
		t.Fatalf("PermanentLockedPrincipal = %s, want %d", result.PermanentLockedPrincipal, permanentPrincipal)
	}

	if !result.Credited.Rewards.Equal(wantRewards) {
		t.Fatalf("Credited.Rewards = %s, want %s", result.Credited.Rewards, wantRewards)
	}

	if !result.Credited.Commission.Equal(wantCommission) {
		t.Fatalf("Credited.Commission = %s, want %s", result.Credited.Commission, wantCommission)
	}
}
