package store_test

import (
	"encoding/binary"
	"strconv"
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/cosmos/iavl"

	"github.com/sentinel-official/sentinel-snapshot/internal/store"
)

type writer struct{ b []byte }

func (w *writer) writeBytes(b []byte) {
	w.b = binary.AppendUvarint(w.b, uint64(len(b)))
	w.b = append(w.b, b...)
}

func (w *writer) writeVarint(i int64) { w.b = binary.AppendVarint(w.b, i) }

func newRehashFixture(t *testing.T, n int) (dbm.DB, []byte) {
	t.Helper()

	db := dbm.NewMemDB()

	tree, err := iavl.NewMutableTree(db, 0, true)
	if err != nil {
		t.Fatal(err)
	}

	for i := range n {
		k := []byte("key/" + strconv.Itoa(i))
		if _, err := tree.Set(k, []byte("val/"+strconv.Itoa(i))); err != nil {
			t.Fatal(err)
		}
	}

	hash, _, err := tree.SaveVersion()
	if err != nil {
		t.Fatal(err)
	}

	return db, hash
}

func newTree(t *testing.T, kv map[string]string) (dbm.DB, []byte) {
	t.Helper()

	db := dbm.NewMemDB()

	tree, err := iavl.NewMutableTree(db, 0, false)
	if err != nil {
		t.Fatal(err)
	}

	for k, v := range kv {
		if _, err := tree.Set([]byte(k), []byte(v)); err != nil {
			t.Fatal(err)
		}
	}

	hash, _, err := tree.SaveVersion()
	if err != nil {
		t.Fatal(err)
	}

	return db, hash
}

func encodeInnerNode(height, size, version int64, key, left, right []byte) []byte {
	var w writer
	w.writeVarint(height)
	w.writeVarint(size)
	w.writeVarint(version)
	w.writeBytes(key)
	w.writeBytes(left)
	w.writeBytes(right)

	return w.b
}

func encodeLeaf(size, version int64, key, value []byte) []byte {
	var w writer
	w.writeVarint(0)
	w.writeVarint(size)
	w.writeVarint(version)
	w.writeBytes(key)
	w.writeBytes(value)

	return w.b
}

func prefixEnd(prefix []byte) []byte {
	end := append([]byte(nil), prefix...)
	for i := len(end) - 1; i >= 0; i-- {
		if end[i] != 0xFF {
			end[i]++

			return end[:i+1]
		}
	}

	return nil
}

func tamperOneLeafValue(t *testing.T, db dbm.DB) {
	t.Helper()

	it, err := db.Iterator([]byte{store.NodeKeyPrefix}, prefixEnd([]byte{store.NodeKeyPrefix}))
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := it.Close(); err != nil {
			t.Errorf("close node iterator: %v", err)
		}
	}()

	for ; it.Valid(); it.Next() {
		raw := append([]byte(nil), it.Value()...)

		size, version, key, value, isLeaf, err := store.ParseNode(raw)
		if err != nil || !isLeaf || len(value) == 0 {
			continue
		}

		value[0] ^= 0xFF
		if err := db.Set(append([]byte(nil), it.Key()...), encodeLeaf(size, version, key, value)); err != nil {
			t.Fatal(err)
		}

		return
	}

	t.Fatal("no leaf node found to tamper")
}
