//go:build integration

package apptest

import (
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"cosmossdk.io/math"
	dbm "github.com/cometbft/cometbft-db"
	abci "github.com/cometbft/cometbft/abci/types"
	tmlog "github.com/cometbft/cometbft/libs/log"
	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/anchor"
	"github.com/sentinel-official/sentinel-snapshot/internal/app"
	"github.com/sentinel-official/sentinel-snapshot/internal/bank"
	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
	sentineltypes "github.com/sentinel-official/sentinelhub/v12/types"
)

const seedChainID = "sentinel-snapshot-seed-test"

//nolint:gochecknoglobals
var (
	seedTime             = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	seedDelegatorAddress = sdk.AccAddress(sha256Sum20("sentinel-snapshot/internal/app.seedDelegatorAddress"))
)

type emptyAppOptions struct{}

func (emptyAppOptions) Get(string) any { return nil }

func SeedBalance(t *testing.T, address sdk.AccAddress, amount math.Int) *app.Handle {
	t.Helper()

	coins := sdk.NewCoins(sdk.NewCoin(bank.DenomUDVPN, amount))

	return SeedHandle(t, func(ctx sdk.Context, application *sentinelapp.App) {
		if err := application.BankKeeper.MintCoins(ctx, minttypes.ModuleName, coins); err != nil {
			t.Fatalf("SeedBalance: MintCoins: %v", err)
		}

		if err := application.BankKeeper.SendCoinsFromModuleToAccount(ctx, minttypes.ModuleName, address, coins); err != nil {
			t.Fatalf("SeedBalance: SendCoinsFromModuleToAccount: %v", err)
		}
	})
}

func SeedHandle(t *testing.T, seed func(ctx sdk.Context, app *sentinelapp.App)) *app.Handle {
	t.Helper()

	db := dbm.NewMemDB()

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close seed database: %v", err)
		}
	})

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
		baseapp.SetChainID(seedChainID),
		baseapp.SetIAVLDisableFastNode(true),
	)

	genesisState := sentinelapp.ModuleBasics.DefaultGenesis(encCfg.Codec)

	validatorSet, err := sims.CreateRandomValidatorSet()
	if err != nil {
		t.Fatalf("SeedHandle: create validator set: %v", err)
	}

	genesisAccount := authtypes.NewBaseAccountWithAddress(seedDelegatorAddress)

	genesisState, err = sims.GenesisStateWithValSet(
		encCfg.Codec,
		genesisState,
		validatorSet,
		[]authtypes.GenesisAccount{genesisAccount},
	)
	if err != nil {
		t.Fatalf("SeedHandle: genesis state with validator set: %v", err)
	}

	stateBytes, err := json.Marshal(genesisState)
	if err != nil {
		t.Fatalf("SeedHandle: marshal default genesis: %v", err)
	}

	application.InitChain(abci.RequestInitChain{
		ChainId:         seedChainID,
		Time:            seedTime,
		ConsensusParams: sims.DefaultConsensusParams,
		AppStateBytes:   stateBytes,
	})

	application.BeginBlock(abci.RequestBeginBlock{
		Header: tmproto.Header{
			ChainID: seedChainID,
			Height:  1,
			Time:    seedTime,
		},
	})

	ctx := application.NewContext(false, tmproto.Header{
		ChainID: seedChainID,
		Height:  1,
		Time:    seedTime,
	})
	seed(ctx, application)

	application.EndBlock(abci.RequestEndBlock{Height: 1})
	application.Commit()

	nextHeight := application.LastBlockHeight() + 1
	application.BeginBlock(abci.RequestBeginBlock{
		Header: tmproto.Header{
			ChainID: seedChainID,
			Height:  nextHeight,
			Time:    seedTime,
		},
	})
	application.EndBlock(abci.RequestEndBlock{Height: nextHeight})
	application.Commit()

	committedHeight := application.LastBlockHeight()

	header := cmttypes.Header{
		Height:  committedHeight + 1,
		AppHash: application.LastCommitID().Hash,
		Time:    seedTime,
	}

	h, err := app.Open(t.Context(), db, anchor.FromHeader(&header), 1)
	if err != nil {
		t.Fatalf("SeedHandle: Open: %v", err)
	}

	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Errorf("close seeded app: %v", err)
		}
	})

	return h
}

func Address(t *testing.T) sdk.AccAddress {
	t.Helper()

	return sdk.AccAddress(sha256Sum20("sentinel-snapshot/internal/app.Address"))
}

func sha256Sum20(s string) []byte {
	sum := sha256.Sum256([]byte(s))

	return sum[:20]
}
