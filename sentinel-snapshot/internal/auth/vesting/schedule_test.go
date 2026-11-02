package vesting_test

import (
	"bytes"
	"testing"

	"github.com/sentinel-official/sentinel-snapshot/internal/auth/vesting"
)

func TestSchedule_EncodePreimage_WireLayout(t *testing.T) {
	t.Parallel()

	s := vesting.Schedule{Kind: vesting.KindPeriodic, StartUnix: 100, EndUnix: 130,
		Periods: []vesting.Period{{10, 40}, {20, 60}}, TotalUDVPN: 100}

	var want bytes.Buffer
	want.WriteByte(2)
	writeI64LE(&want, 100)
	writeI64LE(&want, 130)
	writeU32LE(&want, 2)
	writeI64LE(&want, 10)
	writeU64LE(&want, 40)
	writeI64LE(&want, 20)
	writeU64LE(&want, 60)
	writeU64LE(&want, 100)

	got, err := s.EncodePreimage()
	if err != nil {
		t.Fatalf("EncodePreimage: %v", err)
	}

	if !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("preimage mismatch")
	}
}

func TestSchedule_Validate_RejectsTooManyPeriods(t *testing.T) {
	t.Parallel()

	periods := make([]vesting.Period, 513)

	var total uint64

	for i := range periods {
		periods[i] = vesting.Period{LengthSeconds: 1, AmountUDVPN: 1}
		total++
	}

	s := vesting.Schedule{Kind: vesting.KindPeriodic, StartUnix: 0, EndUnix: 513, Periods: periods, TotalUDVPN: total}
	if err := s.Validate(total); err == nil {
		t.Fatal("513-period schedule must fail validation")
	}
}
