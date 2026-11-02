package vesting_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/sentinel-official/sentinel-snapshot/internal/auth/vesting"
)

func assertReanchorInvariants(t *testing.T, original vesting.Original, locked uint64, haltUnix int64) {
	t.Helper()

	schedule, hasSchedule := vesting.Reanchor(original, locked, haltUnix)
	if locked == 0 {
		return
	}

	if !hasSchedule {
		t.Fatalf("expected a schedule for L=%d", locked)
	}

	if schedule.TotalUDVPN != locked {
		t.Fatalf("schedule total %d != L %d", schedule.TotalUDVPN, locked)
	}

	if err := schedule.Validate(locked); err != nil {
		t.Fatalf("re-anchored schedule fails validation: %v", err)
	}

	if schedule.UnlockedAt(haltUnix) != 0 {
		t.Fatalf("re-anchored schedule must unlock 0 at the halt time, got %d", schedule.UnlockedAt(haltUnix))
	}

	vested := unlockedOriginal(original, haltUnix)
	for unix := haltUnix; unix <= schedule.EndUnix+2; unix++ {
		got := schedule.UnlockedAt(unix)
		originalUnlocked := unlockedOriginal(original, unix)

		var want uint64
		if originalUnlocked > vested {
			want = min(locked, originalUnlocked-vested)
		}

		diff := int64(got) - int64(want)
		if diff < -1 || diff > 1 {
			t.Fatalf("t=%d: rebased %d vs min(L, orig−V) %d (>1 apart)", unix, got, want)
		}
	}
}

func unlockedOriginal(original vesting.Original, unix int64) uint64 {
	return original.ToSchedule().UnlockedAt(unix)
}

func writeI64LE(b *bytes.Buffer, v int64) {
	var t [8]byte
	binary.LittleEndian.PutUint64(t[:], uint64(v))
	b.Write(t[:])
}

func writeU32LE(b *bytes.Buffer, v uint32) {
	var t [4]byte
	binary.LittleEndian.PutUint32(t[:], v)
	b.Write(t[:])
}

func writeU64LE(b *bytes.Buffer, v uint64) {
	var t [8]byte
	binary.LittleEndian.PutUint64(t[:], v)
	b.Write(t[:])
}
