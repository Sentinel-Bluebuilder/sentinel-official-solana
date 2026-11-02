//go:build integration

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	dbm "github.com/cometbft/cometbft-db"
	tmlog "github.com/cometbft/cometbft/libs/log"
	"github.com/cosmos/cosmos-sdk/baseapp"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/anchor"
	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
	sentineltypes "github.com/sentinel-official/sentinelhub/v12/types"
)

type scratchHome struct {
	dir  string
	err  error
	once sync.Once
}

func newReadOnlyApp(db dbm.DB) (*sentinelapp.App, *scratchHome, error) {
	homeDir, err := os.MkdirTemp("", "sentinelhub-app-")
	if err != nil {
		return nil, nil, fmt.Errorf("app: scratch home dir: %w", err)
	}

	encCfg := sentinelapp.DefaultEncodingConfig()
	application := sentinelapp.NewApp(
		emptyAppOptions{},
		sentineltypes.Bech32MainPrefix,
		db,
		encCfg,
		homeDir,
		0,
		true,
		tmlog.NewNopLogger(),
		false,
		nil,
		"",
		nil,
		nil,
		baseapp.SetIAVLDisableFastNode(true),
	)

	return application, &scratchHome{dir: homeDir}, nil
}

type Handle struct {
	storeNames    []string
	ctx           sdk.Context
	blockTimeUnix int64
	app           *sentinelapp.App
	verified      bool
	stateVersion  int64
	db            dbm.DB
	scratch       *scratchHome
}

func (h *Handle) App() *sentinelapp.App { return h.app }

func (h *Handle) BlockTimeUnix() int64 { return h.blockTimeUnix }

func (h *Handle) Close() error {
	h.scratch.once.Do(func() {
		if err := os.RemoveAll(h.scratch.dir); err != nil {
			h.scratch.err = fmt.Errorf("remove app scratch home: %w", err)
		}
	})

	return h.scratch.err
}

func (h *Handle) Context() sdk.Context { return h.ctx }

func (h *Handle) StoreNames() []string { return h.storeNames }

func (h *Handle) Verified() bool { return h.verified }

func (h *Handle) WithNewContext() (*Handle, error) {
	c, err := h.app.CreateQueryContext(h.stateVersion, false)
	if err != nil {
		return nil, fmt.Errorf("create query context: %w", err)
	}

	nh := *h
	nh.ctx = c.WithBlockTime(time.Unix(h.blockTimeUnix, 0).UTC())

	return &nh, nil
}

func Open(ctx context.Context, db dbm.DB, h anchor.Halt, workers int) (_ *Handle, retErr error) {
	application, scratch, err := newReadOnlyApp(db)
	if err != nil {
		return nil, err
	}

	defer func() {
		if retErr != nil {
			handle := &Handle{scratch: scratch}
			retErr = errors.Join(retErr, handle.Close())
		}
	}()

	storeNames := kvStoreNames(application)

	verified, err := verifyArchiveGate(ctx, db, h.StateVersion(), storeNames, h.AppHash, workers)
	if err != nil {
		return nil, err
	}

	sdkCtx, err := application.CreateQueryContext(h.StateVersion(), false)
	if err != nil {
		return nil, fmt.Errorf("create query context: %w", err)
	}

	sdkCtx = sdkCtx.WithBlockTime(time.Unix(h.BlockTimeUnix(), 0).UTC())

	return &Handle{
		storeNames:    storeNames,
		ctx:           sdkCtx,
		blockTimeUnix: h.BlockTimeUnix(),
		app:           application,
		verified:      verified,
		stateVersion:  h.StateVersion(),
		db:            db,
		scratch:       scratch,
	}, nil
}

func kvStoreNames(application *sentinelapp.App) []string {
	kv := application.KVKeys()

	names := make([]string, 0, len(kv))
	for name := range kv {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}

type emptyAppOptions struct{}

func (emptyAppOptions) Get(string) any { return nil }
