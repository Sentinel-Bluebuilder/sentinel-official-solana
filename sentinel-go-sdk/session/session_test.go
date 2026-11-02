package session_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
)

func TestBasis_MarshalText(t *testing.T) {
	t.Parallel()

	basis, err := session.ParseBasis("per_hour")
	require.NoError(t, err)
	require.Equal(t, session.BasisPerHour, basis)
	require.Equal(t, "per_hour", basis.String())

	text, err := session.BasisPerGB.MarshalText()
	require.NoError(t, err)
	require.Equal(t, "per_gb", string(text))

	_, err = session.ParseBasis("per_day")
	require.ErrorIs(t, err, session.ErrInvalidBasis)
}

func TestHeadroom(t *testing.T) {
	t.Parallel()

	require.Equal(t, uint64(50), session.Headroom(150, 100))
	require.Zero(t, session.Headroom(100, 150))
	require.Zero(t, session.Headroom(100, 100))
	require.Equal(t, uint64(math.MaxUint64), session.Headroom(math.MaxUint64, 0))
}

func TestOwed_Properties(t *testing.T) {
	t.Parallel()

	check := func(t *rapid.T) {
		price := rapid.Uint64().Draw(t, "price")
		a := rapid.Uint64().Draw(t, "a")
		b := rapid.Uint64().Draw(t, "b")

		owedA := session.Owed(session.BasisPerGB, price, a)
		owedB := session.Owed(session.BasisPerGB, price, b)

		if a <= b && owedA > owedB {
			t.Fatalf("owed not monotonic: %d > %d", owedA, owedB)
		}

		if price == 0 && owedA != 0 {
			t.Fatalf("free basis owes %d", owedA)
		}
	}

	rapid.Check(t, check)
}

func TestOwed_RoundsUp(t *testing.T) {
	t.Parallel()

	require.EqualValues(t, 0, session.Owed(session.BasisPerGB, 100, 0))
	require.EqualValues(t, 1, session.Owed(session.BasisPerGB, 100, 1))
	require.EqualValues(t, 100, session.Owed(session.BasisPerGB, 100, session.BytesPerGB))
	require.EqualValues(t, 50, session.Owed(session.BasisPerHour, 100, 1_800))
	require.Equal(t, uint64(math.MaxUint64), session.Owed(session.BasisPerGB, math.MaxUint64, math.MaxUint64))
}

func TestTerms_Validate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		terms   session.Terms
		wantErr error
	}{
		{name: "per gb", terms: session.Terms{Basis: session.BasisPerGB, Price: 1}},
		{name: "per hour", terms: session.Terms{Basis: session.BasisPerHour, Price: 1}},
		{name: "free", terms: session.Terms{Basis: session.BasisPerGB}},
		{name: "zero basis", wantErr: session.ErrInvalidBasis},
		{name: "unknown basis", terms: session.Terms{Basis: 9}, wantErr: session.ErrInvalidBasis},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.ErrorIs(t, tc.terms.Validate(), tc.wantErr)
		})
	}
}

func TestTolerance(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		local   uint64
		block   uint64
		percent uint8
		want    uint64
	}{
		{name: "bytes block", block: 100_000_000, percent: 5, want: 5_000_000},
		{name: "seconds block", block: 60, percent: 5, want: 3},
		{name: "local and block", local: 10_000_000_000, block: 100_000_000, percent: 5, want: 505_000_000},
		{name: "round up", local: 1, percent: 5, want: 1},
		{name: "zero percent", local: math.MaxUint64, block: math.MaxUint64},
		{name: "zero usage", percent: 5},
		{
			name:    "sum exceeds uint64",
			local:   math.MaxUint64,
			block:   math.MaxUint64,
			percent: 5,
			want:    1_844_674_407_370_955_162,
		},
		{name: "exact maximum", local: math.MaxUint64, percent: 100, want: math.MaxUint64},
		{
			name:    "ceil to maximum",
			local:   math.MaxUint64,
			block:   186_330_748_219_288_400,
			percent: 99,
			want:    math.MaxUint64,
		},
		{
			name:    "ceil overflow saturates",
			local:   math.MaxUint64,
			block:   186_330_748_219_288_401,
			percent: 99,
			want:    math.MaxUint64,
		},
		{name: "saturate", local: math.MaxUint64, block: math.MaxUint64, percent: 255, want: math.MaxUint64},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, session.Tolerance(tc.local, tc.block, tc.percent))
		})
	}
}

func TestUsage_Amount(t *testing.T) {
	t.Parallel()

	usage := session.Usage{
		RxBytes: 10,
		Seconds: 7,
		TxBytes: 5,
	}
	require.EqualValues(t, 15, usage.Amount(session.BasisPerGB))
	require.EqualValues(t, 7, usage.Amount(session.BasisPerHour))

	usage = session.Usage{
		RxBytes: math.MaxUint64,
		TxBytes: 1,
	}
	require.Equal(t, uint64(math.MaxUint64), usage.Amount(session.BasisPerGB))
}
