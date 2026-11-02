//go:build integration

package auth_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/apptest"
	"github.com/sentinel-official/sentinel-snapshot/internal/auth"
	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
)

//nolint:paralleltest
func TestClassifyAccounts_EscrowOverride(t *testing.T) {
	escrow := newAddress("escrow-like-no-pub-key")
	plain := newAddress("plain-user-no-pub-key")
	h := apptest.SeedHandle(t, func(ctx sdk.Context, application *sentinelapp.App) {
		seedBalance(t, ctx, application, escrow, math.NewInt(500))
		seedBalance(t, ctx, application, plain, math.NewInt(700))
	})

	baseline, err := auth.ClassifyAccounts(t.Context(), h, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got := classOf(t, baseline, escrow); got != auth.ClassLeafUnrevealed {
		t.Fatalf("escrow baseline class = %v, want LeafUnrevealed", got)
	}

	escrowAddresses := map[string]bool{escrow.String(): true}

	overridden, err := auth.ClassifyAccounts(t.Context(), h, escrowAddresses)
	if err != nil {
		t.Fatal(err)
	}

	if got := classOf(t, overridden, escrow); got != auth.ClassTreasury {
		t.Fatalf("escrow override class = %v, want Treasury", got)
	}

	if got := classOf(t, overridden, plain); got != auth.ClassLeafUnrevealed {
		t.Fatalf("plain class = %v, want LeafUnrevealed (must not be affected)", got)
	}
}

//nolint:paralleltest
func TestClassifyAccounts(t *testing.T) {
	revealedPrivKey := secp256k1.GenPrivKey()
	revealedAddress := sdk.AccAddress(revealedPrivKey.PubKey().Address())
	unrevealedAddress := apptest.Address(t)

	h := apptest.SeedHandle(t, func(ctx sdk.Context, application *sentinelapp.App) {
		application.AccountKeeper.GetModuleAccount(ctx, minttypes.ModuleName)

		revealedAccount := application.AccountKeeper.NewAccountWithAddress(ctx, revealedAddress)
		if err := revealedAccount.SetPubKey(revealedPrivKey.PubKey()); err != nil {
			t.Fatalf("SetPubKey: %v", err)
		}

		application.AccountKeeper.SetAccount(ctx, revealedAccount)

		unrevealedAccount := application.AccountKeeper.NewAccountWithAddress(ctx, unrevealedAddress)
		application.AccountKeeper.SetAccount(ctx, unrevealedAccount)
	})

	accounts, err := auth.ClassifyAccounts(t.Context(), h, nil)
	if err != nil {
		t.Fatalf("ClassifyAccounts: %v", err)
	}

	byAddress := make(map[string]auth.Account, len(accounts))
	for _, account := range accounts {
		byAddress[account.AddressBech32] = account
	}

	moduleAddress := authtypes.NewModuleAddress(minttypes.ModuleName)

	moduleAccount, ok := byAddress[moduleAddress.String()]
	if !ok {
		t.Fatalf("module account %s not found in ClassifyAccounts output", moduleAddress)
	}

	if moduleAccount.Class != auth.ClassTreasury {
		t.Fatalf("module account class = %v, want Treasury", moduleAccount.Class)
	}

	if moduleAccount.CompressedPubKey != nil {
		t.Fatalf("module account CompressedPubKey = %x, want nil", moduleAccount.CompressedPubKey)
	}

	revealedAccount, ok := byAddress[revealedAddress.String()]
	if !ok {
		t.Fatalf("revealed account %s not found in ClassifyAccounts output", revealedAddress)
	}

	if revealedAccount.Class != auth.ClassLeafRevealed {
		t.Fatalf("revealed account class = %v, want LeafRevealed", revealedAccount.Class)
	}

	if len(revealedAccount.CompressedPubKey) != 33 {
		t.Fatalf("revealed account CompressedPubKey len = %d, want 33", len(revealedAccount.CompressedPubKey))
	}

	unrevealedAccount, ok := byAddress[unrevealedAddress.String()]
	if !ok {
		t.Fatalf("never-revealed account %s not found in ClassifyAccounts output", unrevealedAddress)
	}

	if unrevealedAccount.Class != auth.ClassLeafUnrevealed {
		t.Fatalf("never-revealed account class = %v, want LeafUnrevealed", unrevealedAccount.Class)
	}

	if unrevealedAccount.CompressedPubKey != nil {
		t.Fatalf("never-revealed account CompressedPubKey = %x, want nil", unrevealedAccount.CompressedPubKey)
	}

	for _, account := range accounts {
		if account.Class == auth.ClassLeafUnrevealed && account.CompressedPubKey != nil {
			t.Fatalf("account %s classified LeafUnrevealed but has a non-nil CompressedPubKey", account.AddressBech32)
		}
	}
}
