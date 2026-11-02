package vesting_test

import (
	"testing"

	"github.com/sentinel-official/sentinel-snapshot/internal/auth/vesting"
)

func TestReanchor_ContinuousShortfall(t *testing.T) {
	t.Parallel()

	original := vesting.Original{Kind: vesting.KindContinuous, StartUnix: 0, EndUnix: 1000, TotalUDVPN: 1000}
	assertReanchorInvariants(t, original, 300, 400)
}

func TestReanchor_PeriodicShortfall(t *testing.T) {
	t.Parallel()

	original := vesting.Original{
		Kind: vesting.KindPeriodic, StartUnix: 0, EndUnix: 400, TotalUDVPN: 1000,
		Periods: []vesting.Period{{100, 250}, {100, 250}, {100, 250}, {100, 250}},
	}
	assertReanchorInvariants(t, original, 750, 150)
	assertReanchorInvariants(t, original, 400, 150)
}
