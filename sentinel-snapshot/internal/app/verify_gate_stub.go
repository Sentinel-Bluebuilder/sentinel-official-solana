//go:build integration && !unsafeskipverify

package app

import (
	"context"

	dbm "github.com/cometbft/cometbft-db"

	"github.com/sentinel-official/sentinel-snapshot/internal/store"
)

func verifyArchiveGate(
	ctx context.Context,
	db dbm.DB,
	version int64,
	storeNames []string,
	appHash []byte,
	workers int,
) (bool, error) {
	if _, err := store.VerifyArchive(ctx, db, version, storeNames, appHash, workers); err != nil {
		return false, err
	}

	return true, nil
}
