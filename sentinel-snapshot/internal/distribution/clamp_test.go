package distribution_test

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/distribution"
)

func TestClampToOutstanding_ReservesCommissionAndBoundsSum(t *testing.T) {
	t.Parallel()

	first := sdk.AccAddress([]byte("aaaaaaaaaaaaaaaaaaaa"))
	second := sdk.AccAddress([]byte("bbbbbbbbbbbbbbbbbbbb"))

	credited, commission := distribution.ClampToOutstanding(
		dec("10"), dec("4"),
		[]distribution.Credit{{Address: second, Amount: dec("5")}, {Address: first, Amount: dec("5")}},
	)
	if !commission.Equal(math.NewInt(4)) {
		t.Fatalf("commission = %s, want 4", commission)
	}

	if !credited[first.String()].Equal(math.NewInt(5)) {
		t.Fatalf("first credited %s, want 5", credited[first.String()])
	}

	if !credited[second.String()].Equal(math.NewInt(1)) {
		t.Fatalf("second credited %s (last in order absorbs the shortfall), want 1", credited[second.String()])
	}

	sum := credited[first.String()].Add(credited[second.String()]).Add(commission)
	if sum.GT(math.NewInt(10)) {
		t.Fatalf("Σ credits + commission = %s exceeds outstanding 10", sum)
	}
}
