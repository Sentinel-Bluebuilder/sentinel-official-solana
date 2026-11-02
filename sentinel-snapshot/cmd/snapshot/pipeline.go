//go:build integration

package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"cosmossdk.io/math"
	dbm "github.com/cometbft/cometbft-db"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	goleveldbopt "github.com/syndtr/goleveldb/leveldb/opt"

	"github.com/sentinel-official/sentinel-snapshot/internal/anchor"
	"github.com/sentinel-official/sentinel-snapshot/internal/app"
	"github.com/sentinel-official/sentinel-snapshot/internal/assemble"
	"github.com/sentinel-official/sentinel-snapshot/internal/ibcescrow"
	"github.com/sentinel-official/sentinel-snapshot/internal/ledger"
	"github.com/sentinel-official/sentinel-snapshot/internal/prune"
	"github.com/sentinel-official/sentinel-snapshot/internal/report"
	"github.com/sentinel-official/sentinel-snapshot/internal/strandcheck"
	"github.com/sentinel-official/sentinel-snapshot/internal/treasury"
	"github.com/sentinel-official/sentinel-snapshot/internal/version"
	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
)

func buildRecords(accounts []assemble.Account) ([]ledger.Record, math.Int, error) {
	records := make([]ledger.Record, 0, len(accounts))
	totalClaimable := math.ZeroInt()

	for _, account := range accounts {
		var schedule []byte

		if account.Schedule != nil {
			var err error

			schedule, err = account.Schedule.EncodePreimage()
			if err != nil {
				return nil, math.Int{}, fmt.Errorf("account %s: %w", account.AddressBech32, err)
			}
		}

		records = append(records, ledger.Record{
			Address:       account.Address,
			AddressBech32: account.AddressBech32,
			PubKey:        account.CompressedPubKey,
			LiquidUDVPN:   account.LiquidUDVPN,
			LockedUDVPN:   account.LockedUDVPN,
			Schedule:      schedule,
		})
		totalClaimable = totalClaimable.
			Add(math.NewIntFromUint64(account.LiquidUDVPN)).
			Add(math.NewIntFromUint64(account.LockedUDVPN))
	}

	return records, totalClaimable, nil
}

func checkArchiveExists(path string) error {
	info, statErr := os.Stat(path)
	if statErr != nil || !info.IsDir() {
		return fmt.Errorf("no such archive: %s", path)
	}

	if _, err := os.Stat(filepath.Join(path, "CURRENT")); err != nil {
		return fmt.Errorf("no such archive: %s", path)
	}

	return nil
}

func openDB(path string) (dbm.DB, error) {
	db, err := dbm.NewDB("application", dbm.BackendType(version.Pinned().DBBackend), filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	return db, nil
}

func openDBReadOnly(path string) (dbm.DB, error) {
	if err := checkArchiveExists(path); err != nil {
		return nil, err
	}

	backend := version.Pinned().DBBackend
	if backend != "goleveldb" {
		return nil, fmt.Errorf("verify: read-only open is only implemented for the goleveldb backend, pinned "+
			"backend is %q", backend)
	}

	db, err := dbm.NewGoLevelDBWithOpts("application", filepath.Dir(path), &goleveldbopt.Options{
		ReadOnly:       true,
		ErrorIfMissing: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open verify database: %w", err)
	}

	return db, nil
}

func runPrune(ctx context.Context, a pruneArgs) (retErr error) {
	authDone := stageTimer(ctx, "authenticate")

	header, err := a.authenticate()
	if err != nil {
		return err
	}

	authDone()

	if a.Height != header.Height {
		return fmt.Errorf("prune --height %d disagrees with authenticated header H height %d", a.Height, header.Height)
	}

	db, err := openDB(a.AppDBPath)
	if err != nil {
		return err
	}

	defer func() {
		if err := db.Close(); err != nil && retErr == nil {
			retErr = fmt.Errorf("close prune database: %w", err)
		}
	}()

	openDone := stageTimer(ctx, "open")

	handle, err := app.Open(ctx, db, anchor.FromHeader(header), a.Workers)
	if err != nil {
		return err
	}

	defer func() {
		if err := handle.Close(); err != nil {
			retErr = errors.Join(retErr, err)
		}
	}()

	openDone()

	if !handle.Verified() {
		return errors.New("prune: IAVL rehash was skipped (unsafeskipverify build) — refusing to delete " +
			"versions from an unverified archive")
	}

	pruneDone := stageTimer(ctx, "prune")
	if err := prune.RetainOnly(ctx, db, header.Height-1, handle.StoreNames()); err != nil {
		return err
	}

	pruneDone()

	return nil
}

func runSnapshot(ctx context.Context, a snapshotArgs) (retErr error) {
	authDone := stageTimer(ctx, "authenticate")

	header, err := a.authenticate()
	if err != nil {
		return err
	}

	authDone()

	db, err := openDB(a.AppDBPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil && retErr == nil {
			retErr = fmt.Errorf("close snapshot database: %w", err)
		}
	}()

	openDone := stageTimer(ctx, "open")

	handle, err := app.Open(ctx, db, anchor.FromHeader(header), a.Workers)
	if err != nil {
		return err
	}

	defer func() {
		if err := handle.Close(); err != nil {
			retErr = errors.Join(retErr, err)
		}
	}()

	openDone()

	escrowDone := stageTimer(ctx, "escrow-enumerate")

	escrows, err := ibcescrow.Channels(handle)
	if err != nil {
		return fmt.Errorf("enumerate escrow: %w", err)
	}

	escrowDone()

	escrowSet := make(map[string]bool, len(escrows))
	for _, e := range escrows {
		escrowSet[e.Address.String()] = true
	}

	assembleDone := stageTimer(ctx, "assemble")

	result, err := assemble.Run(ctx, handle, escrowSet, a.Workers)
	if err != nil {
		return err
	}

	assembleDone()

	records, totalClaimable, err := buildRecords(result.Accounts)
	if err != nil {
		return err
	}

	gatherDone := stageTimer(ctx, "treasury-gather")

	components, err := treasury.Gather(ctx, handle, treasury.GatherInput{
		Credited:                 result.Credited,
		PermanentLockedPrincipal: result.PermanentLockedPrincipal,
		NonLeafAttributedEscrow:  result.NonLeafAttributedEscrow,
		Escrows:                  escrows,
		ClassifiedAccounts:       result.ClassifiedAccounts,
		BalanceByAddress:         result.BalanceByAddress,
		DelegatedByAddress:       result.DelegatedByAddress,
		UnbondingByAddress:       result.UnbondingByAddress,
	})
	if err != nil {
		return err
	}

	gatherDone()

	slog.InfoContext(ctx, "ibc fee escrow empty — confirmed")

	treasuryTotal := components.Total()
	reconcileDone := stageTimer(ctx, "reconcile")

	err = treasury.Reconcile(result.SupplyAtHalt, totalClaimable, treasuryTotal)
	if err != nil {
		return err
	}

	reconcileDone()

	familyByAddress := make(map[[20]byte]string)

	for name := range sentinelapp.ModuleAccPerms() {
		var k [20]byte
		copy(k[:], authtypes.NewModuleAddress(name))
		familyByAddress[k] = "module"
	}

	for _, e := range escrows {
		var k [20]byte
		copy(k[:], e.Address)
		familyByAddress[k] = "escrow"
	}

	strandDone := stageTimer(ctx, "strandcheck")

	err = strandcheck.ValidateNoStrandedUnrevealed(records, familyByAddress)
	if err != nil {
		return err
	}

	strandDone()

	encodeDone := stageTimer(ctx, "encode")

	out, err := ledger.Encode(records)
	if err != nil {
		return err
	}

	encodeDone()

	writeDone := stageTimer(ctx, "write")

	//nolint:gosec
	if err := os.WriteFile(a.LedgerPath, out, 0o644); err != nil {
		return fmt.Errorf("write ledger: %w", err)
	}

	writeDone()

	contentHash := out[len(out)-32:]

	reconciliationPath := a.ReconciliationPath
	if reconciliationPath == "" {
		reconciliationPath = filepath.Join(filepath.Dir(a.LedgerPath), "reconciliation.json")
	}

	reconciliation := report.New(
		result.SupplyAtHalt,
		totalClaimable,
		treasuryTotal,
		hex.EncodeToString(contentHash),
		components.IBCFeeEscrow,
		components.NonLeafAttributedEscrow,
		components.IBCEscrowByChannel,
		handle.Verified(),
	)
	if !reconciliation.Verified {
		slog.WarnContext(ctx, "reconciliation records verified=false: archive rehash skipped (unsafeskipverify build)")
	}

	b, err := reconciliation.Marshal()
	if err != nil {
		return err
	}

	//nolint:gosec
	if err := os.WriteFile(reconciliationPath, b, 0o644); err != nil {
		return fmt.Errorf("write reconciliation: %w", err)
	}

	return nil
}

func runVerify(ctx context.Context, a verifyArgs) (retErr error) {
	authDone := stageTimer(ctx, "authenticate")

	header, err := a.authenticate()
	if err != nil {
		return err
	}

	authDone()

	db, err := openDBReadOnly(a.AppDBPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil && retErr == nil {
			retErr = fmt.Errorf("close verify database: %w", err)
		}
	}()

	openDone := stageTimer(ctx, "open")

	handle, err := app.Open(ctx, db, anchor.FromHeader(header), a.Workers)
	if err != nil {
		return err
	}

	defer func() {
		if err := handle.Close(); err != nil {
			retErr = errors.Join(retErr, err)
		}
	}()

	openDone()

	if !handle.Verified() {
		return errors.New("verify: IAVL rehash was skipped (unsafeskipverify build) — refusing to report success")
	}

	slog.InfoContext(ctx, "verify succeeded: commit over header H is >2/3-signed and the recomputed "+
		"AppHash matches it at H-1",
		"height", header.Height)

	return nil
}

func stageTimer(ctx context.Context, stage string) func() {
	t0 := time.Now()

	slog.InfoContext(ctx, "stage start", "stage", stage)

	return func() {
		slog.InfoContext(ctx, "stage done", "stage", stage, "duration", time.Since(t0).String())
	}
}
