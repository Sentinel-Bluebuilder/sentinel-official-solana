package session_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
)

func TestPayer_Audit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		localBytes  uint64
		name        string
		nodeBytes   uint64
		nodeOwed    uint64
		nodeSettled uint64
		settled     uint64
		wantErr     error
	}{
		{
			name: "four percent drift over ten gigabytes passes", localBytes: 10_000_000_000,
			nodeBytes: 10_400_000_000, nodeOwed: 10_400,
		},
		{name: "start of session noise passes", nodeBytes: 4_000_000, nodeOwed: 4},
		{name: "tolerance boundary passes", nodeBytes: 5_000_000, nodeOwed: 5},
		{
			localBytes: 10_000_000_000,
			name:       "six percent drift fails",
			nodeBytes:  10_600_000_000,
			nodeOwed:   10_600,
			wantErr:    session.ErrUsageMismatch,
		},
		{name: "settled moved", nodeSettled: 1, wantErr: session.ErrSettledChanged},
		{name: "owed inconsistent", nodeOwed: 1, wantErr: session.ErrOwedMismatch},
		{name: "settled included in owed", settled: 200, nodeSettled: 200, nodeBytes: 4_000_000, nodeOwed: 204},
		{
			name:        "settled omitted from owed",
			nodeBytes:   4_000_000,
			nodeOwed:    4,
			nodeSettled: 200,
			settled:     200,
			wantErr:     session.ErrOwedMismatch,
		},
		{name: "local ahead passes", localBytes: 20_000_000, nodeBytes: 10_000_000, nodeOwed: 10},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			payer := session.NewPayer(session.Terms{Basis: session.BasisPerGB, Price: 1_000}, 100_000_000, 1_000, 5)
			payer.Resume(300, tc.settled)

			err := payer.Audit(session.Usage{RxBytes: tc.localBytes}, session.Usage{TxBytes: tc.nodeBytes},
				tc.nodeSettled, tc.nodeOwed)

			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestPayer_Audit_Seconds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		seconds uint64
		wantErr error
	}{
		{name: "within tolerance", seconds: 22},
		{name: "at tolerance", seconds: 24},
		{name: "above tolerance", seconds: 25, wantErr: session.ErrUsageMismatch},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			payer := session.NewPayer(session.Terms{Basis: session.BasisPerHour, Price: 3_600}, 60, 1_000, 5)
			err := payer.Audit(session.Usage{Seconds: 20}, session.Usage{Seconds: tc.seconds}, 0, tc.seconds)

			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestPayer_Next(t *testing.T) {
	t.Parallel()

	cases := []struct {
		issue      bool
		localBytes uint64
		name       string
		nodeBytes  uint64
		paid       uint64
		settled    uint64
		want       uint64
	}{
		{name: "fresh block untouched", paid: 100, localBytes: 30_000_000, nodeBytes: 30_000_000, want: 100},
		{name: "half block consumed issues", paid: 100, localBytes: 50_000_000, want: 150, issue: true},
		{
			issue:      true,
			localBytes: 50_000_000,
			name:       "node ahead sizes from node",
			nodeBytes:  52_000_000,
			paid:       100,
			want:       152,
		},
		{
			issue:      true,
			localBytes: 60_000_000,
			name:       "local ahead sizes from local",
			nodeBytes:  50_000_000,
			paid:       100,
			want:       160,
		},
		{name: "capped at escrow", paid: 950, localBytes: 900_000_000, want: 1_000, issue: true},
		{name: "paid equals escrow never issues", paid: 1_000, localBytes: 999_000_000, want: 1_000},
		{name: "paid above escrow never issues", paid: 1_100, localBytes: 999_000_000, want: 1_100},
		{name: "settled carries over", paid: 300, settled: 200, localBytes: 60_000_000, want: 360, issue: true},
		{name: "one unit above threshold holds", paid: 100, localBytes: 49_000_000, want: 100},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			payer := session.NewPayer(session.Terms{Basis: session.BasisPerGB, Price: 1_000}, 100_000_000, 1_000, 5)
			payer.Resume(tc.paid, tc.settled)

			cumulative, issue := payer.Next(session.Usage{RxBytes: tc.localBytes}, session.Usage{TxBytes: tc.nodeBytes})

			require.Equal(t, tc.want, cumulative)
			require.Equal(t, tc.issue, issue)
			require.Equal(t, tc.paid, payer.Paid())
		})
	}
}

func TestPayer_Next_Saturation(t *testing.T) {
	t.Parallel()

	terms := session.Terms{Basis: session.BasisPerHour, Price: 3_600}
	payer := session.NewPayer(terms, 100, math.MaxUint64, 5)
	payer.Resume(math.MaxUint64-50, math.MaxUint64-10)

	cumulative, issue := payer.Next(session.Usage{Seconds: 20}, session.Usage{})
	require.True(t, issue)
	require.Equal(t, uint64(math.MaxUint64), cumulative)

	cumulative, err := payer.Opening(math.MaxUint64-50, 100)
	require.NoError(t, err)
	require.Equal(t, uint64(math.MaxUint64), cumulative)

	err = payer.Audit(session.Usage{Seconds: math.MaxUint64 - 1}, session.Usage{Seconds: math.MaxUint64},
		math.MaxUint64-10, math.MaxUint64)
	require.NoError(t, err)
}

func TestPayer_Next_SecondsRounding(t *testing.T) {
	t.Parallel()

	payer := session.NewPayer(session.Terms{Basis: session.BasisPerHour, Price: 100}, 60, 1_000, 5)
	payer.Resume(2, 0)

	cumulative, issue := payer.Next(session.Usage{Seconds: 1}, session.Usage{})
	require.True(t, issue)
	require.EqualValues(t, 3, cumulative)

	cumulative, err := payer.Opening(0, 60)
	require.NoError(t, err)
	require.EqualValues(t, 2, cumulative)
}

func TestPayer_Opening(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		paid    uint64
		owed    uint64
		want    uint64
		wantErr error
	}{
		{name: "fresh channel", want: 100},
		{name: "surplus reused", paid: 300, owed: 150, want: 300},
		{name: "surplus below block tops up", paid: 300, owed: 250, want: 350},
		{name: "escrow tail accepted at half minimum", owed: 950, want: 1_000},
		{name: "escrow tail rejected", owed: 960, want: 1_000, wantErr: session.ErrEscrowExhausted},
		{name: "owed above escrow", owed: 1_001, want: 1_000, wantErr: session.ErrEscrowExhausted},
		{name: "existing paid capped at escrow", paid: 1_100, want: 1_000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			payer := session.NewPayer(session.Terms{Basis: session.BasisPerGB, Price: 1_000}, 100_000_000, 1_000, 5)
			payer.Resume(tc.paid, 0)

			cumulative, err := payer.Opening(tc.owed, 100_000_000)

			require.Equal(t, tc.want, cumulative)
			require.ErrorIs(t, err, tc.wantErr)
			require.Equal(t, tc.paid, payer.Paid())
		})
	}
}

func TestPayer_Opening_Free(t *testing.T) {
	t.Parallel()

	payer := session.NewPayer(session.Terms{Basis: session.BasisPerHour}, 60, 0, 5)

	cumulative, issue := payer.Next(session.Usage{Seconds: 60}, session.Usage{})
	require.False(t, issue)
	require.Zero(t, cumulative)

	cumulative, err := payer.Opening(math.MaxUint64, 60)
	require.NoError(t, err)
	require.Zero(t, cumulative)
}

func TestPayer_Opening_RejectsInsufficientBlock(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		block   uint64
		name    string
		price   uint64
		wantErr error
	}{
		{block: 0, name: "zero block", price: 1_000, wantErr: session.ErrPrepayZero},
		{block: 100_000_000, name: "single unit block", price: 10, wantErr: session.ErrPrepayTooSmall},
		{block: 200_000_000, name: "two unit block", price: 10},
		{block: 100_000_000, name: "free block"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			payer := session.NewPayer(session.Terms{Basis: session.BasisPerGB, Price: tc.price}, tc.block, 1_000, 5)
			_, err := payer.Opening(0, 0)

			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestPayer_Resume_MonotonicPaid(t *testing.T) {
	t.Parallel()

	payer := session.NewPayer(session.Terms{Basis: session.BasisPerGB, Price: 1_000}, 100_000_000, 1_000, 5)
	cumulative, issue := payer.Next(session.Usage{}, session.Usage{})
	require.True(t, issue)
	require.EqualValues(t, 100, cumulative)

	payer.RecordIssued(cumulative)
	payer.RecordIssued(50)
	payer.Resume(75, 200)

	cumulative, issue = payer.Next(session.Usage{}, session.Usage{})
	require.True(t, issue)
	require.EqualValues(t, 300, cumulative)
	require.NoError(t, payer.Audit(session.Usage{}, session.Usage{}, 200, 200))
	require.EqualValues(t, 100, payer.Paid())

	payer.Resume(400, 0)

	require.EqualValues(t, 400, payer.Paid())
	require.NoError(t, payer.Audit(session.Usage{}, session.Usage{}, 0, 0))
}

func TestPrepayBlock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		configured uint64
		maximum    uint64
		minimum    uint64
		name       string
		terms      session.Terms
		want       uint64
		wantErr    error
	}{
		{
			maximum: 2_000_000_000,
			minimum: 1,
			name:    "per gb tiny node minimum",
			terms:   session.Terms{Basis: session.BasisPerGB, Price: 5_000},
			want:    200_001,
		},
		{
			maximum: 14_400,
			minimum: 1,
			name:    "per hour tiny node minimum",
			terms:   session.Terms{Basis: session.BasisPerHour, Price: 100},
			want:    37,
		},
		{
			configured: 100_000_000,
			maximum:    2_000_000_000,
			minimum:    1,
			name:       "configured above the floor",
			terms:      session.Terms{Basis: session.BasisPerGB, Price: 5_000},
			want:       100_000_000,
		},
		{
			maximum: 2_000_000_000,
			minimum: 1,
			name:    "free terms keep the minimum",
			terms:   session.Terms{Basis: session.BasisPerGB},
			want:    1,
		},
		{
			maximum: 2_000_000_000,
			name:    "free terms zero block",
			terms:   session.Terms{Basis: session.BasisPerGB},
			wantErr: session.ErrPrepayZero,
		},
		{
			maximum: 1_000,
			minimum: 1,
			name:    "maximum below the floor",
			terms:   session.Terms{Basis: session.BasisPerGB, Price: 5_000},
			wantErr: session.ErrPrepayTooSmall,
		},
		{
			maximum: 2_000_000_000,
			minimum: 3_000_000_000,
			name:    "minimum above maximum",
			terms:   session.Terms{Basis: session.BasisPerGB, Price: 5_000},
			wantErr: session.ErrPrepayExceedsMaximum,
		},
		{
			maximum: math.MaxUint64,
			minimum: 1,
			name:    "price above the unit",
			terms:   session.Terms{Basis: session.BasisPerGB, Price: math.MaxUint64},
			want:    1,
		},
		{
			maximum: math.MaxUint64,
			name:    "price of one",
			terms:   session.Terms{Basis: session.BasisPerGB, Price: 1},
			want:    1_000_000_001,
		},
		{
			configured: math.MaxUint64,
			maximum:    math.MaxUint64,
			name:       "maximum amounts",
			terms:      session.Terms{Basis: session.BasisPerHour, Price: math.MaxUint64},
			want:       math.MaxUint64,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			block, err := session.PrepayBlock(tc.terms, tc.configured, tc.minimum, tc.maximum)

			require.ErrorIs(t, err, tc.wantErr)
			require.Equal(t, tc.want, block)
		})
	}
}
