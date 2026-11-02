package staking_test

import (
	"testing"

	"cosmossdk.io/math"

	"github.com/sentinel-official/sentinel-snapshot/internal/staking"
)

func TestTokensFromShares(t *testing.T) {
	t.Parallel()

	got := staking.TokensFromShares(dec("10.5"), math.NewInt(100), dec("21"))
	if !got.Equal(math.NewInt(50)) {
		t.Fatalf("exact case = %s, want 50", got)
	}

	got = staking.TokensFromShares(dec("1"), math.NewInt(100), dec("3"))
	if !got.Equal(math.NewInt(33)) {
		t.Fatalf("truncated case = %s, want 33", got)
	}
}

func TestValidatorPoolDust(t *testing.T) {
	t.Parallel()

	dust, err := staking.ValidatorPoolDust(math.NewInt(100), []math.Int{math.NewInt(33), math.NewInt(33), math.NewInt(33)})
	if err != nil {
		t.Fatal(err)
	}

	if !dust.Equal(math.NewInt(1)) {
		t.Fatalf("dust = %s, want 1", dust)
	}

	dust, err = staking.ValidatorPoolDust(math.NewInt(0), nil)
	if err != nil {
		t.Fatal(err)
	}

	if !dust.IsZero() {
		t.Fatalf("slashed dust = %s, want 0", dust)
	}
}
