package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	dbm "github.com/cometbft/cometbft-db"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	"github.com/cometbft/cometbft/state"
	"github.com/cometbft/cometbft/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "anchorgen: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) (retErr error) {
	fs := flag.NewFlagSet("anchorgen", flag.ContinueOnError)

	var (
		dataDir = fs.String("data-dir", "", "path to the node's data/ dir (containing blockstore.db and state.db)")
		outDir  = fs.String("out-dir", "anchor", "output dir for header.json/commit.json/validator-set.json")
		height  = fs.Int64("height", 0, "halt height H (default: blockstore tip)")
		chainID = fs.String("chain-id", "", "if set, assert the loaded header's chain id equals this")
	)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse anchorgen flags: %w", err)
	}

	if *dataDir == "" {
		return errors.New("requires --data-dir")
	}

	blockDB, err := dbm.NewDB("blockstore", dbm.GoLevelDBBackend, *dataDir)
	if err != nil {
		return fmt.Errorf("open blockstore.db: %w", err)
	}
	defer func() {
		if err := blockDB.Close(); err != nil && retErr == nil {
			retErr = fmt.Errorf("close blockstore.db: %w", err)
		}
	}()

	stateDB, err := dbm.NewDB("state", dbm.GoLevelDBBackend, *dataDir)
	if err != nil {
		return fmt.Errorf("open state.db: %w", err)
	}
	defer func() {
		if err := stateDB.Close(); err != nil && retErr == nil {
			retErr = fmt.Errorf("close state.db: %w", err)
		}
	}()

	blockStore := store.NewBlockStore(blockDB)

	haltHeight := *height
	if haltHeight == 0 {
		haltHeight = store.LoadBlockStoreState(blockDB).Height
		if haltHeight == 0 {
			return errors.New("blockstore is empty (height 0); pass --height explicitly")
		}

		fmt.Fprintf(os.Stderr, "anchorgen: using blockstore tip H=%d\n", haltHeight)
	}

	meta := blockStore.LoadBlockMeta(haltHeight)
	if meta == nil {
		return fmt.Errorf("no block meta at height %d in blockstore", haltHeight)
	}

	header := meta.Header
	if *chainID != "" && header.ChainID != *chainID {
		return fmt.Errorf("header chain id %q != --chain-id %q", header.ChainID, *chainID)
	}

	commit := blockStore.LoadSeenCommit(haltHeight)
	if commit == nil {
		return fmt.Errorf("no seen commit at height %d in blockstore", haltHeight)
	}

	stateStore := state.NewStore(stateDB, state.StoreOptions{})

	validators, err := stateStore.LoadValidators(haltHeight)
	if err != nil {
		return fmt.Errorf("load validators at height %d: %w", haltHeight, err)
	}

	//nolint:gosec
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", *outDir, err)
	}

	if err := writeJSON(filepath.Join(*outDir, "header.json"), &header); err != nil {
		return err
	}

	if err := writeJSON(filepath.Join(*outDir, "commit.json"), commit); err != nil {
		return err
	}

	if err := writeJSON(filepath.Join(*outDir, "validator-set.json"), validators); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "anchorgen: wrote anchor for H=%d chain_id=%q to %s\n", haltHeight, header.ChainID, *outDir)

	return nil
}

func writeJSON(path string, v any) error {
	b, err := cmtjson.Marshal(v)
	if err != nil {
		return fmt.Errorf("cmtjson.Marshal(%s): %w", path, err)
	}

	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}
