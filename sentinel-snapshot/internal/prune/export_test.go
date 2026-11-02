//go:build integration

package prune

import (
	"context"

	dbm "github.com/cometbft/cometbft-db"
)

func PrefixEndByte(b byte) []byte { return prefixEndByte(b) }

func StripFastNodes(ctx context.Context, db dbm.DB) error { return stripFastNodes(ctx, db) }
