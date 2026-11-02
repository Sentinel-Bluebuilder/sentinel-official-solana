//go:build integration && unsafeskipverify

package app

import (
	"context"
	"log/slog"

	dbm "github.com/cometbft/cometbft-db"
)

func verifyArchiveGate(_ context.Context, _ dbm.DB, version int64, _ []string, _ []byte, _ int) (bool, error) {
	slog.Warn("UNSAFE BUILD: IAVL rehash SKIPPED — archive NOT verified against header H's AppHash",
		"build_tag", "unsafeskipverify",
		"state_version", version)

	return false, nil
}
