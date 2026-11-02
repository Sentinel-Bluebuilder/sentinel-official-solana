//go:build integration

package store_test

import (
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/cometbft/cometbft/libs/log"
	"github.com/cosmos/cosmos-sdk/store/rootmulti"
	storetypes "github.com/cosmos/cosmos-sdk/store/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/store"
)

func TestVerifyArchive_MatchesMultistore(t *testing.T) {
	t.Parallel()

	db := dbm.NewMemDB()
	ms := rootmulti.NewStore(db, log.NewNopLogger())
	kAcc := storetypes.NewKVStoreKey("acc")
	kBank := storetypes.NewKVStoreKey("bank")

	ms.MountStoreWithDB(kAcc, storetypes.StoreTypeIAVL, nil)
	ms.MountStoreWithDB(kBank, storetypes.StoreTypeIAVL, nil)

	if err := ms.LoadLatestVersion(); err != nil {
		t.Fatal(err)
	}

	ms.GetKVStore(kAcc).Set([]byte("a1"), []byte("v1"))
	ms.GetKVStore(kBank).Set([]byte("b1"), []byte("v1"))
	commit := ms.Commit()

	roots, err := store.VerifyArchive(t.Context(), db, commit.Version, []string{"acc", "bank"}, commit.Hash, 1)
	if err != nil {
		t.Fatalf("VerifyArchive: %v", err)
	}

	if len(roots) != 2 {
		t.Fatalf("got %d roots", len(roots))
	}
}
