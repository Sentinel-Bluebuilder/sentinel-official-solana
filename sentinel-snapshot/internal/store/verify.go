//go:build integration

package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/cosmos/iavl"

	"github.com/sentinel-official/sentinel-snapshot/internal/progress"
)

type prefixGetter struct {
	db     dbm.DB
	prefix []byte
}

func (g *prefixGetter) Get(key []byte) ([]byte, error) {
	full := make([]byte, len(g.prefix)+len(key))
	copy(full, g.prefix)
	copy(full[len(g.prefix):], key)

	value, err := g.db.Get(full)
	if err != nil {
		return nil, fmt.Errorf("get prefixed node: %w", err)
	}

	return value, nil
}

func VerifyArchive(
	ctx context.Context,
	db dbm.DB,
	version int64,
	storeNames []string,
	appHash []byte,
	workers int,
) (map[string][]byte, error) {
	roots := make(map[string][]byte, len(storeNames))
	for _, name := range storeNames {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("verify archive: %w", err)
		}

		prefix := dbm.NewPrefixDB(db, []byte("s/k:"+name+"/"))

		tree, err := iavl.NewMutableTree(prefix, 0, true)
		if err != nil {
			return nil, fmt.Errorf("store %q create tree: %w", name, err)
		}

		if _, err := tree.LoadVersion(version); err != nil {
			return nil, fmt.Errorf("store %q load v%d: %w", name, version, err)
		}

		claimed, err := tree.Hash()
		if err != nil {
			return nil, fmt.Errorf("store %q hash: %w", name, err)
		}

		var (
			add  func(int64)
			prog *progress.Tracker
		)

		if workers > 1 {
			total := 2*tree.Size() - 1
			if tree.Size() <= 0 {
				total = 0
			}

			prog = progress.New(ctx, slog.Default(), "rehash "+name, total)
			add = prog.Add
		}

		getter := &prefixGetter{
			db:     db,
			prefix: []byte("s/k:" + name + "/"),
		}
		recomputed, err := RehashTreeParallel(ctx, getter, claimed, workers, add)

		if prog != nil {
			prog.Stop()
		}

		if err != nil {
			if errors.Is(err, ErrMissingNode) {
				return nil, fmt.Errorf("store %q rehash: archive is missing a required IAVL node — the archive is "+
					"likely truncated or corrupted: %w", name, err)
			}

			return nil, fmt.Errorf("store %q rehash: %w", name, err)
		}

		roots[name] = recomputed
	}

	got := CommitHash(version, roots)
	if !bytes.Equal(got, appHash) {
		return nil, fmt.Errorf("recomputed AppHash %x != header AppHash %x", got, appHash)
	}

	return roots, nil
}
