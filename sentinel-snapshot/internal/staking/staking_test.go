//go:build integration

package staking_test

import (
	"testing"
	"time"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/app"
	"github.com/sentinel-official/sentinel-snapshot/internal/apptest"
	"github.com/sentinel-official/sentinel-snapshot/internal/staking"
	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
)

const (
	seededValidatorTokens = 1_000_000
	seededValidatorShares = 1_000_000
	seededDelegatedShares = 250_000
	seededUnbonding       = 42_000
)

func seedDelegation(t *testing.T, address sdk.AccAddress) *app.Handle {
	t.Helper()

	operator := sdk.ValAddress(address)
	initialBalance := math.NewInt(50_000)

	return apptest.SeedHandle(t, func(ctx sdk.Context, application *sentinelapp.App) {
		validator, err := stakingtypes.NewValidator(operator, ed25519.GenPrivKey().PubKey(), stakingtypes.Description{})
		if err != nil {
			t.Fatalf("NewValidator: %v", err)
		}

		validator.Tokens = math.NewInt(seededValidatorTokens)
		validator.DelegatorShares = math.LegacyNewDec(seededValidatorShares)
		application.StakingKeeper.SetValidator(ctx, validator)

		delegation := stakingtypes.NewDelegation(address, operator, math.LegacyNewDec(seededDelegatedShares))
		application.StakingKeeper.SetDelegation(ctx, delegation)

		unbonding := stakingtypes.NewUnbondingDelegation(
			address, operator, ctx.BlockHeight(), ctx.BlockTime().Add(time.Hour), initialBalance, 1,
		)
		unbonding.Entries[0].Balance = math.NewInt(seededUnbonding)
		application.StakingKeeper.SetUnbondingDelegation(ctx, unbonding)
	})
}

//nolint:paralleltest
func TestDelegatedTokens(t *testing.T) {
	address := apptest.Address(t)
	want := staking.TokensFromShares(
		math.LegacyNewDec(seededDelegatedShares),
		math.NewInt(seededValidatorTokens),
		math.LegacyNewDec(seededValidatorShares),
	)

	h := seedDelegation(t, address)

	got, err := staking.DelegatedTokens(h, address)
	if err != nil {
		t.Fatalf("DelegatedTokens: %v", err)
	}

	if !got.Equal(want) {
		t.Fatalf("DelegatedTokens = %s, want %s", got, want)
	}
}

//nolint:paralleltest
func TestUnbondingTokens(t *testing.T) {
	address := apptest.Address(t)

	want := math.NewInt(seededUnbonding)

	h := seedDelegation(t, address)
	if got := staking.UnbondingTokens(h, address); !got.Equal(want) {
		t.Fatalf("UnbondingTokens = %s, want %s", got, want)
	}
}
