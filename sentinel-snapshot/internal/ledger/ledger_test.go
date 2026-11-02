package ledger_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"golang.org/x/crypto/sha3"

	"github.com/sentinel-official/sentinel-snapshot/internal/ledger"
)

func TestEncode_LockedScheduleInvariant(t *testing.T) {
	t.Parallel()

	pubKey := make([]byte, 33)

	pubKey[0] = 0x02
	if _, err := ledger.Encode([]ledger.Record{newRecord(0x11, pubKey, 1, 5, nil)}); err == nil {
		t.Fatal("LockedUDVPN>0 with nil Schedule must be rejected")
	}

	if _, err := ledger.Encode([]ledger.Record{newRecord(0x11, pubKey, 1, 0, newSchedule(0))}); err == nil {
		t.Fatal("LockedUDVPN==0 with a Schedule must be rejected")
	}
}

func TestEncode_GoldenLayout(t *testing.T) {
	t.Parallel()

	pubKey := make([]byte, 33)
	pubKey[0] = 0x02
	records := []ledger.Record{
		newRecord(0x22, nil, 5, 0, nil),
		newRecord(0x11, pubKey, 100, 40, newSchedule(40)),
	}

	var payload bytes.Buffer
	payload.WriteString(ledger.Magic)

	var v4 [4]byte
	binary.LittleEndian.PutUint32(v4[:], ledger.FormatVersion)
	payload.Write(v4[:])

	var c8 [8]byte
	binary.LittleEndian.PutUint64(c8[:], 2)
	payload.Write(c8[:])
	writeRecordBytes(&payload, records[1])
	writeRecordBytes(&payload, records[0])

	h := sha3.NewLegacyKeccak256()
	h.Write(payload.Bytes())
	want := append(payload.Bytes(), h.Sum(nil)...)

	got, err := ledger.Encode(records)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("ledger bytes mismatch\n got %x\nwant %x", got, want)
	}
}

func TestEncode_DuplicateAddress(t *testing.T) {
	t.Parallel()

	pubKey := make([]byte, 33)
	pubKey[0] = 0x02

	_, err := ledger.Encode([]ledger.Record{
		newRecord(0x11, pubKey, 1, 0, nil),
		newRecord(0x11, pubKey, 2, 0, nil),
	})
	if err == nil {
		t.Fatal("duplicate 20-byte address must be rejected")
	}
}
