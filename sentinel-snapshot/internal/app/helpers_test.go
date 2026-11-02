//go:build integration

package app_test

import (
	"fmt"
	"sync/atomic"
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/anchor"
	"github.com/sentinel-official/sentinel-snapshot/internal/app"
	"github.com/sentinel-official/sentinel-snapshot/internal/apptest"
	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
)

type recordingDB struct {
	dbm.DB

	closes atomic.Int64
}

func newRecordingHandle(t *testing.T) (*app.Handle, *recordingDB) {
	t.Helper()

	seeded := apptest.SeedHandle(t, func(sdk.Context, *sentinelapp.App) {})
	db := &recordingDB{DB: app.HandleDB(seeded)}
	header := cmttypes.Header{
		AppHash: seeded.App().LastCommitID().Hash,
		Height:  seeded.App().LastBlockHeight() + 1,
		Time:    seeded.Context().BlockTime(),
	}

	if err := seeded.Close(); err != nil {
		t.Fatal(err)
	}

	h, err := app.Open(t.Context(), db, anchor.FromHeader(&header), 1)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Errorf("close recording handle: %v", err)
		}
	})

	return h, db
}

func (db *recordingDB) Close() error {
	db.closes.Add(1)

	if err := db.DB.Close(); err != nil {
		return fmt.Errorf("close recording database: %w", err)
	}

	return nil
}
