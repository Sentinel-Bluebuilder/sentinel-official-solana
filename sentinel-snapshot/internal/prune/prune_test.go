//go:build integration

package prune_test

import (
	"context"
	"errors"
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/cosmos/iavl"

	"github.com/sentinel-official/sentinel-snapshot/internal/prune"
)

func TestRetainOnly_DropsOlderVersions(t *testing.T) {
	t.Parallel()

	db := seedStore(t, 3)
	if err := prune.RetainOnly(t.Context(), db, 3, []string{"bank"}); err != nil {
		t.Fatal(err)
	}

	prefix := dbm.NewPrefixDB(db, []byte("s/k:bank/"))

	tree, err := iavl.NewMutableTree(prefix, 0, true)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := tree.LoadVersion(3); err != nil {
		t.Fatalf("version 3 should be retained: %v", err)
	}

	if tree.VersionExists(1) || tree.VersionExists(2) {
		t.Fatal("versions 1,2 should be pruned")
	}
}

func TestStripFastNodes_ClearsFastNodesAndStorageVersion(t *testing.T) {
	t.Parallel()

	db := seedStore(t, 1)

	prefix := dbm.NewPrefixDB(db, []byte("s/k:bank/"))
	if err := prune.StripFastNodes(t.Context(), prefix); err != nil {
		t.Fatal(err)
	}

	iterator, err := prefix.Iterator([]byte{'f'}, prune.PrefixEndByte('f'))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = iterator.Close() }()

	if iterator.Valid() {
		t.Fatal("fast-node 'f' records should be stripped")
	}

	has, err := prefix.Has(append([]byte{'m'}, []byte("storage_version")...))
	if err != nil {
		t.Fatal(err)
	}

	if has {
		t.Fatal("'m' storage_version metadata should be cleared")
	}
}

func TestStripFastNodes_Cancellation(t *testing.T) {
	t.Parallel()

	for _, stage := range []string{"scan", "scan end", "write"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			db := dbm.NewMemDB()
			for _, key := range []string{"f1", "f2", "mstorage_version"} {
				if err := db.Set([]byte(key), []byte("value")); err != nil {
					t.Fatal(err)
				}
			}

			wrapped := &fakeCancelingDB{DB: db, cancel: cancel, stage: stage}
			if err := prune.StripFastNodes(ctx, wrapped); !errors.Is(err, context.Canceled) {
				t.Fatalf("expected cancellation during %s, got %v", stage, err)
			}

			for _, key := range []string{"f1", "f2", "mstorage_version"} {
				if stage == "write" && key != "mstorage_version" {
					continue
				}

				has, err := db.Has([]byte(key))
				if err != nil {
					t.Fatal(err)
				}

				if !has {
					t.Fatalf("canceled operation deleted %s", key)
				}
			}
		})
	}
}
