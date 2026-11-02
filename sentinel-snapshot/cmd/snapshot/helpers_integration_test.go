//go:build integration

package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	tmlog "github.com/cometbft/cometbft/libs/log"
	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/bank"
	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
	sentineltypes "github.com/sentinel-official/sentinelhub/v12/types"
)

const testChainID = "sentinel-test"

type emptyAppOptions struct{}

func (emptyAppOptions) Get(string) any { return nil }

func seedArchive(t *testing.T, seed func(ctx sdk.Context, app *sentinelapp.App)) snapshotArgs {
	t.Helper()
	dir := t.TempDir()

	db, err := dbm.NewDB("application", dbm.GoLevelDBBackend, dir)
	if err != nil {
		t.Fatalf("dbm.NewDB: %v", err)
	}

	encCfg := sentinelapp.DefaultEncodingConfig()
	application := sentinelapp.NewApp(
		emptyAppOptions{},
		sentineltypes.Bech32MainPrefix,
		db,
		encCfg,
		t.TempDir(),
		0,
		true,
		tmlog.NewNopLogger(),
		false,
		nil,
		"test",
		nil,
		nil,
		baseapp.SetChainID(testChainID),
		baseapp.SetIAVLDisableFastNode(true),
	)

	haltAt := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	genesisState := sentinelapp.ModuleBasics.DefaultGenesis(encCfg.Codec)

	validatorSet, err := sims.CreateRandomValidatorSet()
	if err != nil {
		t.Fatalf("CreateRandomValidatorSet: %v", err)
	}

	delegatorAddress := sdk.AccAddress(sha256Sum20("cmd/snapshot.testArchiveGenesisDelegator"))
	genesisAccount := authtypes.NewBaseAccountWithAddress(delegatorAddress)

	genesisState, err = genesisStateWithUDVPNValidator(
		encCfg.Codec,
		genesisState,
		validatorSet,
		[]authtypes.GenesisAccount{genesisAccount},
	)
	if err != nil {
		t.Fatalf("genesisStateWithUDVPNValidator: %v", err)
	}

	stateBytes, err := json.Marshal(genesisState)
	if err != nil {
		t.Fatalf("marshal genesis: %v", err)
	}

	application.InitChain(abci.RequestInitChain{
		ChainId:         testChainID,
		Time:            haltAt,
		ConsensusParams: sims.DefaultConsensusParams,
		AppStateBytes:   stateBytes,
	})
	application.BeginBlock(abci.RequestBeginBlock{
		Header: tmproto.Header{ChainID: testChainID, Height: 1, Time: haltAt},
	})
	ctx := application.NewContext(false, tmproto.Header{ChainID: testChainID, Height: 1, Time: haltAt})

	if seed != nil {
		seed(ctx, application)
	}

	application.EndBlock(abci.RequestEndBlock{Height: 1})
	application.Commit()

	committedHeight := application.LastBlockHeight()
	appHash := application.LastCommitID().Hash

	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	anchorValidators, privValidators := cmttypes.RandValidatorSet(1, 10)
	header := cmttypes.Header{
		ChainID:        testChainID,
		Height:         committedHeight + 1,
		Time:           haltAt,
		AppHash:        appHash,
		ValidatorsHash: anchorValidators.Hash(),
	}
	blockID := cmttypes.BlockID{
		Hash:          header.Hash(),
		PartSetHeader: cmttypes.PartSetHeader{Total: 1, Hash: sha256Sum32("cmd/snapshot.testArchivePartSetHeader")},
	}
	commit := newSignedCommit(t, testChainID, anchorValidators, privValidators, header.Height, blockID, haltAt)

	headerPath, commitPath, validatorSetPath := writeAnchorFiles(t, dir, &header, commit, anchorValidators)

	return snapshotArgs{
		haltArgs: haltArgs{
			HeaderPath:       headerPath,
			CommitPath:       commitPath,
			ValidatorSetPath: validatorSetPath,
			ChainID:          testChainID,
		},
		AppDBPath:          filepath.Join(dir, "application.db"),
		LedgerPath:         filepath.Join(dir, "ledger.bin"),
		ReconciliationPath: filepath.Join(dir, "reconciliation.json"),
	}
}

func genesisStateWithUDVPNValidator(
	cdc codec.Codec,
	genesisState map[string]json.RawMessage,
	validatorSet *cmttypes.ValidatorSet,
	genesisAccounts []authtypes.GenesisAccount,
) (map[string]json.RawMessage, error) {
	genesisState, err := sims.GenesisStateWithValSet(cdc, genesisState, validatorSet, genesisAccounts)
	if err != nil {
		return nil, fmt.Errorf("genesis state with validator set: %w", err)
	}

	var stakingGen stakingtypes.GenesisState
	cdc.MustUnmarshalJSON(genesisState[stakingtypes.ModuleName], &stakingGen)
	stakingGen.Params.BondDenom = bank.DenomUDVPN
	genesisState[stakingtypes.ModuleName] = cdc.MustMarshalJSON(&stakingGen)

	var bankGen banktypes.GenesisState
	cdc.MustUnmarshalJSON(genesisState[banktypes.ModuleName], &bankGen)

	for i := range bankGen.Balances {
		for j := range bankGen.Balances[i].Coins {
			if bankGen.Balances[i].Coins[j].Denom == sdk.DefaultBondDenom {
				bankGen.Balances[i].Coins[j].Denom = bank.DenomUDVPN
			}
		}
	}

	for i := range bankGen.Supply {
		if bankGen.Supply[i].Denom == sdk.DefaultBondDenom {
			bankGen.Supply[i].Denom = bank.DenomUDVPN
		}
	}

	genesisState[banktypes.ModuleName] = cdc.MustMarshalJSON(&bankGen)

	return genesisState, nil
}

func newSignedCommit(
	t *testing.T, chainID string, validatorSet *cmttypes.ValidatorSet, privValidators []cmttypes.PrivValidator,
	height int64, blockID cmttypes.BlockID, now time.Time,
) *cmttypes.Commit {
	t.Helper()

	voteSet := cmttypes.NewVoteSet(chainID, height, 0, tmproto.PrecommitType, validatorSet)

	commit, err := cmttypes.MakeCommit(blockID, height, 0, voteSet, privValidators, now)
	if err != nil {
		t.Fatalf("MakeCommit: %v", err)
	}

	return commit
}

func sha256Sum20(s string) []byte {
	sum := sha256.Sum256([]byte(s))

	return sum[:20]
}

func sha256Sum32(s string) []byte {
	sum := sha256.Sum256([]byte(s))

	return sum[:]
}

func writeAnchorFiles(
	t *testing.T,
	dir string,
	header *cmttypes.Header,
	commit *cmttypes.Commit,
	validators *cmttypes.ValidatorSet,
) (headerPath, commitPath, validatorSetPath string) {
	t.Helper()

	headerPath = filepath.Join(dir, "header.json")
	commitPath = filepath.Join(dir, "commit.json")
	validatorSetPath = filepath.Join(dir, "validator-set.json")

	writeJSON(t, headerPath, header)
	writeJSON(t, commitPath, commit)
	writeJSON(t, validatorSetPath, validators)

	return headerPath, commitPath, validatorSetPath
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()

	b, err := cmtjson.Marshal(v)
	if err != nil {
		t.Fatalf("cmtjson.Marshal(%s): %v", path, err)
	}

	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
