//go:build integration

package prune

import (
	"context"
	"fmt"

	dbm "github.com/cometbft/cometbft-db"
	"github.com/cosmos/iavl"
)

func RetainOnly(ctx context.Context, db dbm.DB, version int64, storeNames []string) error {
	for _, name := range storeNames {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("prune canceled: %w", err)
		}

		prefix := dbm.NewPrefixDB(db, []byte("s/k:"+name+"/"))

		tree, err := iavl.NewMutableTree(prefix, 0, true)
		if err != nil {
			return fmt.Errorf("create tree %s: %w", name, err)
		}

		if _, err := tree.LoadVersion(version); err != nil {
			return fmt.Errorf("load version %d for %s: %w", version, name, err)
		}

		if err := tree.DeleteVersionsRange(1, version); err != nil {
			return fmt.Errorf("delete old versions for %s: %w", name, err)
		}

		if err := stripFastNodes(ctx, prefix); err != nil {
			return err
		}
	}

	return nil
}

func prefixEndByte(prefixByte byte) []byte {
	if prefixByte == 0xFF {
		return nil
	}

	return []byte{prefixByte + 1}
}

func stripFastNodes(ctx context.Context, prefix dbm.DB) (err error) {
	const (
		fastKeyPrefix = 'f'
		flushEvery    = 100_000
	)

	iterator, err := prefix.Iterator([]byte{fastKeyPrefix}, prefixEndByte(fastKeyPrefix))
	if err != nil {
		return fmt.Errorf("iterate fast nodes: %w", err)
	}
	defer func() {
		if closeErr := iterator.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close fast node iterator: %w", closeErr)
		}
	}()

	batch := prefix.NewBatch()

	defer func() {
		if closeErr := batch.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close fast node batch: %w", closeErr)
		}
	}()

	deleted := 0

	for ; iterator.Valid(); iterator.Next() {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("strip fast nodes canceled: %w", err)
		}

		if err := batch.Delete(iterator.Key()); err != nil {
			return fmt.Errorf("delete fast node: %w", err)
		}

		deleted++
		if deleted%flushEvery == 0 {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("strip fast nodes canceled: %w", err)
			}

			if err := batch.Write(); err != nil {
				return fmt.Errorf("write fast node batch: %w", err)
			}

			batch = prefix.NewBatch()

			if err := ctx.Err(); err != nil {
				return fmt.Errorf("strip fast nodes canceled: %w", err)
			}
		}
	}

	if err := iterator.Error(); err != nil {
		return fmt.Errorf("iterate fast nodes: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("strip fast nodes canceled: %w", err)
	}

	if err := batch.Write(); err != nil {
		return fmt.Errorf("write fast node batch: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("strip fast nodes canceled: %w", err)
	}

	if err := prefix.Delete(append([]byte{'m'}, []byte("storage_version")...)); err != nil {
		return fmt.Errorf("delete storage version metadata: %w", err)
	}

	return nil
}
