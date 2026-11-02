//go:build integration

package prune_test

import (
	"context"
	"fmt"
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/cosmos/iavl"
)

type fakeCancelingBatch struct {
	dbm.Batch

	cancel context.CancelFunc
}

func (batch *fakeCancelingBatch) Write() error {
	err := batch.Batch.Write()
	batch.cancel()

	if err != nil {
		return fmt.Errorf("write cancellation fixture: %w", err)
	}

	return nil
}

type fakeCancelingDB struct {
	dbm.DB

	cancel context.CancelFunc
	stage  string
}

func (db *fakeCancelingDB) Iterator(start, end []byte) (dbm.Iterator, error) {
	iterator, err := db.DB.Iterator(start, end)
	if err != nil {
		return nil, fmt.Errorf("iterate cancellation fixture: %w", err)
	}

	return &fakeCancelingIterator{Iterator: iterator, cancel: db.cancel, stage: db.stage}, nil
}

func (db *fakeCancelingDB) NewBatch() dbm.Batch {
	if db.stage == "write" {
		return &fakeCancelingBatch{Batch: db.DB.NewBatch(), cancel: db.cancel}
	}

	return db.DB.NewBatch()
}

type fakeCancelingIterator struct {
	dbm.Iterator

	cancel context.CancelFunc
	stage  string
}

func (iterator *fakeCancelingIterator) Next() {
	iterator.Iterator.Next()

	if iterator.stage == "scan" || (iterator.stage == "scan end" && !iterator.Valid()) {
		iterator.cancel()
	}
}

func seedStore(t *testing.T, versions int64) dbm.DB {
	t.Helper()

	db := dbm.NewMemDB()
	prefix := dbm.NewPrefixDB(db, []byte("s/k:bank/"))

	tree, err := iavl.NewMutableTree(prefix, 0, false)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := tree.Load(); err != nil {
		t.Fatal(err)
	}

	for i := int64(1); i <= versions; i++ {
		if _, err := tree.Set([]byte{byte(i)}, []byte{byte(i)}); err != nil {
			t.Fatal(err)
		}

		if _, _, err := tree.SaveVersion(); err != nil {
			t.Fatal(err)
		}
	}

	return db
}
