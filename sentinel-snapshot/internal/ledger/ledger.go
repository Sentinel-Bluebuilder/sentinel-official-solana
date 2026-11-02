package ledger

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"slices"

	"github.com/cosmos/cosmos-sdk/types/bech32"
	"golang.org/x/crypto/sha3"
)

const (
	Magic                = "SENTLEDG"
	FormatVersion uint32 = 1
)

type Record struct {
	Address       [20]byte
	AddressBech32 string
	PubKey        []byte
	LiquidUDVPN   uint64
	LockedUDVPN   uint64
	Schedule      []byte
}

func Encode(records []Record) ([]byte, error) {
	sorted := slices.Clone(records)
	slices.SortFunc(sorted, func(a, b Record) int {
		return bytes.Compare(a.Address[:], b.Address[:])
	})

	var payload bytes.Buffer
	payload.WriteString(Magic)
	writeU32(&payload, FormatVersion)
	writeU64(&payload, uint64(len(sorted)))

	for i, r := range sorted {
		if i > 0 && sorted[i-1].Address == r.Address {
			return nil, fmt.Errorf("duplicate address in ledger: %s", r.AddressBech32)
		}

		if r.LiquidUDVPN == 0 && r.LockedUDVPN == 0 {
			return nil, fmt.Errorf("zero-entitlement record for %s must be pruned before encode", r.AddressBech32)
		}

		if (r.LockedUDVPN > 0) != (r.Schedule != nil) {
			return nil, fmt.Errorf("LockedUDVPN/Schedule invariant violated for %s (locked=%d, schedule=%v)",
				r.AddressBech32, r.LockedUDVPN, r.Schedule != nil)
		}

		if r.PubKey != nil && len(r.PubKey) != 33 {
			return nil, fmt.Errorf("pub key for %s must be 33 bytes, got %d", r.AddressBech32, len(r.PubKey))
		}

		if r.PubKey != nil && r.PubKey[0] != 0x02 && r.PubKey[0] != 0x03 {
			return nil, fmt.Errorf("pub key for %s must be compressed (prefix 0x02/0x03), got 0x%02x",
				r.AddressBech32, r.PubKey[0])
		}

		hrp, data, err := bech32.DecodeAndConvert(r.AddressBech32)
		if err != nil {
			return nil, fmt.Errorf("bech32 %q undecodable: %w", r.AddressBech32, err)
		}

		if hrp != "sent" {
			return nil, fmt.Errorf("bech32 %q has HRP %q, want sent", r.AddressBech32, hrp)
		}

		if !bytes.Equal(data, r.Address[:]) {
			return nil, fmt.Errorf("bech32 %q decodes to %x, not Address %x", r.AddressBech32, data, r.Address)
		}

		canon, err := bech32.ConvertAndEncode(hrp, data)
		if err != nil || canon != r.AddressBech32 {
			return nil, fmt.Errorf("bech32 %q is not the canonical lowercase encoding", r.AddressBech32)
		}

		if r.Schedule != nil {
			if len(r.Schedule) < 29 {
				return nil, fmt.Errorf("schedule for %s too short (%d bytes, min 29)", r.AddressBech32, len(r.Schedule))
			}

			n := binary.LittleEndian.Uint32(r.Schedule[17:21])
			if uint64(len(r.Schedule)) != 29+16*uint64(n) {
				return nil, fmt.Errorf("schedule for %s declares %d periods but is %d bytes (want %d)",
					r.AddressBech32, n, len(r.Schedule), 29+16*uint64(n))
			}

			total := binary.LittleEndian.Uint64(r.Schedule[len(r.Schedule)-8:])
			if total != r.LockedUDVPN {
				return nil, fmt.Errorf("schedule total %d != LockedUDVPN %d for %s", total, r.LockedUDVPN, r.AddressBech32)
			}
		}

		if err := writeRecord(&payload, r); err != nil {
			return nil, err
		}
	}

	sum := ContentHash(payload.Bytes())

	return append(payload.Bytes(), sum[:]...), nil
}

func boundedUint32Len(n int, what string) (uint32, error) {
	if uint64(n) > math.MaxUint32 {
		return 0, fmt.Errorf("%s length %d exceeds uint32 wire-format bound", what, n)
	}

	return uint32(n), nil
}

func writeRecord(buf *bytes.Buffer, r Record) error {
	bech32Bytes, err := boundedUint32Len(len(r.AddressBech32), "bech32")
	if err != nil {
		return fmt.Errorf("record %s: %w", r.AddressBech32, err)
	}

	writeU32(buf, bech32Bytes)
	buf.WriteString(r.AddressBech32)

	if r.PubKey == nil {
		buf.WriteByte(0x00)
	} else {
		buf.WriteByte(0x01)
		buf.Write(r.PubKey)
	}

	writeU64(buf, r.LiquidUDVPN)
	writeU64(buf, r.LockedUDVPN)

	if r.Schedule == nil {
		buf.WriteByte(0x00)
	} else {
		buf.WriteByte(0x01)
		buf.Write(r.Schedule)
	}

	return nil
}

func writeU32(buf *bytes.Buffer, v uint32) {
	var t [4]byte
	binary.LittleEndian.PutUint32(t[:], v)
	buf.Write(t[:])
}

func writeU64(buf *bytes.Buffer, v uint64) {
	var t [8]byte
	binary.LittleEndian.PutUint64(t[:], v)
	buf.Write(t[:])
}

func ContentHash(payload []byte) [32]byte {
	h := sha3.NewLegacyKeccak256()
	_, _ = h.Write(payload)

	var out [32]byte
	copy(out[:], h.Sum(nil))

	return out
}
