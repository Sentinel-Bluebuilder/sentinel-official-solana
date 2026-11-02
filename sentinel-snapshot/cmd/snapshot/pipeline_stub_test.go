//go:build integration && !unsafeskipverify

package main

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/bank"
	"github.com/sentinel-official/sentinel-snapshot/internal/report"
	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
)

//nolint:paralleltest
func TestRunSnapshot_GateBeforeWrite(t *testing.T) {
	a := seedArchive(t, func(ctx sdk.Context, application *sentinelapp.App) {
		coins := sdk.NewCoins(sdk.NewCoin(bank.DenomUDVPN, math.NewInt(77)))
		if err := application.BankKeeper.MintCoins(ctx, minttypes.ModuleName, coins); err != nil {
			t.Fatalf("MintCoins: %v", err)
		}
	})

	if err := runSnapshot(t.Context(), a); err == nil {
		t.Fatal("runSnapshot should have failed the reconciliation gate for the unclassified mint-module balance")
	}

	if _, err := os.Stat(a.LedgerPath); !os.IsNotExist(err) {
		t.Fatalf("ledger file %s: want does-not-exist, got err=%v", a.LedgerPath, err)
	}

	if _, err := os.Stat(a.ReconciliationPath); !os.IsNotExist(err) {
		t.Fatalf("reconciliation file %s: want does-not-exist, got err=%v", a.ReconciliationPath, err)
	}
}

//nolint:paralleltest
func TestRunSnapshot(t *testing.T) {
	const fundAmount = 12_345

	fundedAddress := sdk.AccAddress(sha256Sum20("cmd/snapshot.TestRunSnapshot.funded"))
	a := seedArchive(t, func(ctx sdk.Context, application *sentinelapp.App) {
		coins := sdk.NewCoins(sdk.NewCoin(bank.DenomUDVPN, math.NewInt(fundAmount)))
		if err := application.BankKeeper.MintCoins(ctx, minttypes.ModuleName, coins); err != nil {
			t.Fatalf("MintCoins: %v", err)
		}

		if err := application.BankKeeper.SendCoinsFromModuleToAccount(
			ctx,
			minttypes.ModuleName,
			fundedAddress,
			coins,
		); err != nil {
			t.Fatalf("SendCoinsFromModuleToAccount: %v", err)
		}
	})

	if err := runSnapshot(t.Context(), a); err != nil {
		t.Fatalf("runSnapshot: %v", err)
	}

	out, err := os.ReadFile(a.LedgerPath)
	if err != nil {
		t.Fatalf("read ledger %s: %v", a.LedgerPath, err)
	}

	if len(out) < 32 {
		t.Fatalf("ledger %s is only %d bytes, too short for a trailing 32-byte hash", a.LedgerPath, len(out))
	}

	wantContentHash := hex.EncodeToString(out[len(out)-32:])

	reconciliationBytes, err := os.ReadFile(a.ReconciliationPath)
	if err != nil {
		t.Fatalf("read reconciliation %s: %v", a.ReconciliationPath, err)
	}

	var reconciliation report.Reconciliation
	if err := json.Unmarshal(reconciliationBytes, &reconciliation); err != nil {
		t.Fatalf("unmarshal reconciliation.json: %v", err)
	}

	if reconciliation.LedgerContentHash != wantContentHash {
		t.Fatalf(
			"reconciliation.json LedgerContentHash = %s, want %s (the ledger's own trailing hash)",
			reconciliation.LedgerContentHash,
			wantContentHash,
		)
	}

	if !reconciliation.Verified {
		t.Fatalf("reconciliation.json from a verified archive must set verified=true:\n%s", reconciliationBytes)
	}

	if reconciliation.IBCFeeEscrow != "0" {
		t.Fatalf("reconciliation.json ibc_fee_escrow = %q, want \"0\" (default archive seeds no "+
			"ibc fee balance)", reconciliation.IBCFeeEscrow)
	}

	supplyAtHalt, ok := math.NewIntFromString(reconciliation.SupplyAtHalt)
	if !ok {
		t.Fatalf("reconciliation.json supply_at_halt %q is not a valid integer", reconciliation.SupplyAtHalt)
	}

	totalClaimable, ok := math.NewIntFromString(reconciliation.TotalClaimable)
	if !ok {
		t.Fatalf("reconciliation.json total_claimable %q is not a valid integer", reconciliation.TotalClaimable)
	}

	treasuryTotal, ok := math.NewIntFromString(reconciliation.TreasuryTotal)
	if !ok {
		t.Fatalf("reconciliation.json treasury_total %q is not a valid integer", reconciliation.TreasuryTotal)
	}

	if !totalClaimable.Add(treasuryTotal).Equal(supplyAtHalt) {
		t.Fatalf("written reconciliation.json is not self-consistent: total_claimable(%s) + "+
			"treasury_total(%s) = %s != supply_at_halt(%s)",
			totalClaimable, treasuryTotal, totalClaimable.Add(treasuryTotal), supplyAtHalt)
	}

	if supplyAtHalt.IsZero() {
		t.Fatal("supply_at_halt is zero — the seeded funded account should have made this a non-degenerate reconciliation")
	}
}

//nolint:paralleltest
func TestRunVerify_ReadOnly(t *testing.T) {
	a := seedArchive(t, nil)
	va := verifyArgs{haltArgs: a.haltArgs, AppDBPath: a.AppDBPath}

	before := archiveFileStats(t, a.AppDBPath)
	if len(before) == 0 {
		t.Fatalf("archive %s has no files to compare — test setup is broken", a.AppDBPath)
	}

	if err := runVerify(t.Context(), va); err != nil {
		t.Fatalf("runVerify: %v", err)
	}

	after := archiveFileStats(t, a.AppDBPath)
	if len(after) != len(before) {
		t.Fatalf("archive %s file count changed: before=%d after=%d (a read-only verify must not create or delete any file)",
			a.AppDBPath, len(before), len(after))
	}

	for name, wantStat := range before {
		gotStat, ok := after[name]
		if !ok {
			t.Fatalf("archive file %s disappeared after runVerify", name)
		}

		if gotStat != wantStat {
			t.Fatalf("archive file %s changed after runVerify: before=%+v after=%+v (a read-only "+
				"verify must not modify the archive)",
				name, wantStat, gotStat)
		}
	}
}
