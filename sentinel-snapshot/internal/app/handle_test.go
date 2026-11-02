//go:build integration

package app_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/anchor"
	"github.com/sentinel-official/sentinel-snapshot/internal/app"
	"github.com/sentinel-official/sentinel-snapshot/internal/apptest"
	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
)

func TestHandle_Close_SharesCleanup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	h, db := newRecordingHandle(t)

	homes, err := filepath.Glob(filepath.Join(dir, "sentinelhub-app-*"))
	if err != nil {
		t.Fatal(err)
	}

	if len(homes) != 1 {
		t.Fatalf("expected one scratch home, got %v", homes)
	}

	copyHandle, err := h.WithNewContext()
	if err != nil {
		t.Fatal(err)
	}

	for _, handle := range []*app.Handle{copyHandle, h, copyHandle} {
		if err := handle.Close(); err != nil {
			t.Fatal(err)
		}
	}

	if got := db.closes.Load(); got != 0 {
		t.Fatalf("Handle.Close closed supplied database %d times", got)
	}

	if _, err := os.Stat(homes[0]); !os.IsNotExist(err) {
		t.Fatalf("scratch home remains: %v", err)
	}

	if _, err := h.WithNewContext(); err != nil {
		t.Fatalf("Close closed shared database: %v", err)
	}
}

//nolint:paralleltest
func TestOpen_QueryHandleAtHMinus1(t *testing.T) {
	h := apptest.SeedHandle(t, func(ctx sdk.Context, app *sentinelapp.App) {})
	if h.Context().BlockHeight() < 1 {
		t.Fatalf("query ctx height = %d, want >= 1 (H-1)", h.Context().BlockHeight())
	}

	if !h.Context().BlockTime().Equal(time.Unix(h.BlockTimeUnix(), 0).UTC()) {
		t.Fatalf("block time %v not overridden to halt time %d", h.Context().BlockTime(), h.BlockTimeUnix())
	}

	if len(h.StoreNames()) == 0 {
		t.Fatal("no store names mounted")
	}
}

func TestOpen_FailureCleansScratchHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	db := &recordingDB{DB: dbm.NewMemDB()}
	if _, err := app.Open(t.Context(), db, anchor.Halt{}, 1); err == nil {
		t.Fatal("expected invalid archive failure")
	}

	homes, err := filepath.Glob(filepath.Join(dir, "sentinelhub-app-*"))
	if err != nil {
		t.Fatal(err)
	}

	if len(homes) != 0 {
		t.Fatalf("failed Open leaked scratch homes: %v", homes)
	}

	if got := db.closes.Load(); got != 0 {
		t.Fatalf("failed Open closed supplied database %d times", got)
	}
}
