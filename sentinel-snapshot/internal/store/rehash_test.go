package store_test

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"

	"github.com/sentinel-official/sentinel-snapshot/internal/store"
)

func TestRehashTree_MatchesIAVLRoot(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		kv   map[string]string
	}{
		{"single key", map[string]string{"a": "1"}},
		{"three keys", map[string]string{"a": "1", "b": "2", "c": "3"}},
		{"six keys", map[string]string{"k0": "v0", "k1": "v1", "k2": "v2", "k3": "v3", "k4": "v4", "k5": "v5"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db, iavlRoot := newTree(t, tc.kv)

			got, err := store.RehashTree(t.Context(), db, iavlRoot)
			if err != nil {
				t.Fatalf("RehashTree: %v", err)
			}

			if !bytes.Equal(got, iavlRoot) {
				t.Fatalf("recomputed root %x != iavl root %x for %v", got, iavlRoot, tc.kv)
			}
		})
	}
}

func TestRehashTreeParallel_MatchesSequential(t *testing.T) {
	t.Parallel()

	db, root := newRehashFixture(t, 5000)

	sequential, err := store.RehashTree(t.Context(), db, root)
	if err != nil {
		t.Fatal(err)
	}

	for _, w := range []int{1, 2, 4, 8} {
		got, err := store.RehashTreeParallel(t.Context(), db, root, w, nil)
		if err != nil {
			t.Fatalf("workers=%d: %v", w, err)
		}

		if !bytes.Equal(got, sequential) {
			t.Fatalf("workers=%d root %x != sequential %x", w, got, sequential)
		}
	}
}

func TestRehashTree_SharedChildDAG(t *testing.T) {
	t.Parallel()

	db := dbm.NewMemDB()

	hash := store.HashLeaf(1, 1, []byte("k"), []byte("v"))
	if err := db.Set(
		append([]byte{store.NodeKeyPrefix}, hash...),
		encodeLeaf(1, 1, []byte("k"), []byte("v")),
	); err != nil {
		t.Fatal(err)
	}

	const depth = 60
	for i := int64(1); i <= depth; i++ {
		size := int64(1) << uint(i)
		key := []byte(fmt.Sprintf("k%d", i))
		raw := encodeInnerNode(i, size, 1, key, hash, hash)

		next := store.HashInner(i, size, 1, key, hash, hash)
		if err := db.Set(append([]byte{store.NodeKeyPrefix}, next...), raw); err != nil {
			t.Fatal(err)
		}

		hash = next
	}

	rootHash := hash

	done := make(chan struct{})

	var (
		firstResult, secondResult []byte
		firstErr, secondErr       error
	)
	go func() {
		firstResult, firstErr = store.RehashTree(t.Context(), db, rootHash)
		secondResult, secondErr = store.RehashTree(t.Context(), db, rootHash)

		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RehashTree did not terminate promptly on a shared-child DAG; memoization appears missing")
	}

	if firstErr != nil {
		t.Fatalf("RehashTree: %v", firstErr)
	}

	if secondErr != nil {
		t.Fatalf("RehashTree (second run): %v", secondErr)
	}

	if len(firstResult) != 32 {
		t.Fatalf("expected 32-byte hash, got %d bytes", len(firstResult))
	}

	if !bytes.Equal(firstResult, secondResult) {
		t.Fatal("RehashTree is not deterministic across runs on the same DAG")
	}
}

func TestRehashTree_TamperedLeaf(t *testing.T) {
	t.Parallel()

	db, iavlRoot := newTree(t, map[string]string{"a": "1", "b": "2", "c": "3"})

	tamperOneLeafValue(t, db)

	got, err := store.RehashTree(t.Context(), db, iavlRoot)
	if err != nil {
		return
	}

	if bytes.Equal(got, iavlRoot) {
		t.Fatal("deep re-hash accepted a tampered leaf; verification is shallow")
	}
}
