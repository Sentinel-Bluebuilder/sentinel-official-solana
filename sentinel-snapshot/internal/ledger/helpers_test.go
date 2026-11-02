package ledger_test

import (
	"bytes"
	"encoding/binary"

	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/sentinel-official/sentinel-snapshot/internal/ledger"
)

func newRecord(addressByte byte, pubKey []byte, liquid, locked uint64, schedule []byte) ledger.Record {
	var a [20]byte
	for i := range a {
		a[i] = addressByte
	}

	b, err := bech32.ConvertAndEncode("sent", a[:])
	if err != nil {
		panic(err)
	}

	return ledger.Record{
		Address:       a,
		AddressBech32: b,
		PubKey:        pubKey,
		LiquidUDVPN:   liquid,
		LockedUDVPN:   locked,
		Schedule:      schedule,
	}
}

func newSchedule(total uint64) []byte {
	b := make([]byte, 29)
	b[0] = 1
	binary.LittleEndian.PutUint64(b[1:9], 0)
	binary.LittleEndian.PutUint64(b[9:17], 100)
	binary.LittleEndian.PutUint32(b[17:21], 0)
	binary.LittleEndian.PutUint64(b[21:29], total)

	return b
}

func writeRecordBytes(buf *bytes.Buffer, r ledger.Record) {
	var t4 [4]byte
	binary.LittleEndian.PutUint32(t4[:], uint32(len(r.AddressBech32)))
	buf.Write(t4[:])
	buf.WriteString(r.AddressBech32)

	if r.PubKey == nil {
		buf.WriteByte(0x00)
	} else {
		buf.WriteByte(0x01)
		buf.Write(r.PubKey)
	}

	var t8 [8]byte
	binary.LittleEndian.PutUint64(t8[:], r.LiquidUDVPN)
	buf.Write(t8[:])
	binary.LittleEndian.PutUint64(t8[:], r.LockedUDVPN)
	buf.Write(t8[:])

	if r.Schedule == nil {
		buf.WriteByte(0x00)
	} else {
		buf.WriteByte(0x01)
		buf.Write(r.Schedule)
	}
}
