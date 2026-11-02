//go:build integration

package app

import (
	"context"

	dbm "github.com/cometbft/cometbft-db"
)

func HandleDB(h *Handle) dbm.DB { return h.db }

func VerifyArchiveGate(
	ctx context.Context,
	db dbm.DB,
	version int64,
	storeNames []string,
	appHash []byte,
	workers int,
) (bool, error) {
	return verifyArchiveGate(ctx, db, version, storeNames, appHash, workers)
}
