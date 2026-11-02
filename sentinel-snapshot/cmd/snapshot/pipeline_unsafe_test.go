//go:build integration && unsafeskipverify

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/sentinel-official/sentinel-snapshot/internal/anchor"
	"github.com/sentinel-official/sentinel-snapshot/internal/report"
)

//nolint:paralleltest
func TestRunVerify_UnsafeBuildRefuses(t *testing.T) {
	a := seedArchive(t, nil)

	err := runVerify(t.Context(), verifyArgs{haltArgs: a.haltArgs, AppDBPath: a.AppDBPath})
	if err == nil || !strings.Contains(err.Error(), "unsafeskipverify") {
		t.Fatalf("runVerify must refuse under skip build, got %v", err)
	}
}

//nolint:paralleltest
func TestRunPrune_UnsafeBuildRefuses(t *testing.T) {
	a := seedArchive(t, nil)

	header, err := anchor.LoadHeader(a.HeaderPath)
	if err != nil {
		t.Fatalf("load header: %v", err)
	}

	err = runPrune(
		t.Context(),
		pruneArgs{haltArgs: a.haltArgs, AppDBPath: a.AppDBPath, Height: header.Height},
	)
	if err == nil || !strings.Contains(err.Error(), "unsafeskipverify") {
		t.Fatalf("runPrune must refuse under skip build, got %v", err)
	}
}

//nolint:paralleltest
func TestRunSnapshot_UnsafeBuildMarksNotVerified(t *testing.T) {
	a := seedArchive(t, nil)
	if err := runSnapshot(t.Context(), a); err != nil {
		t.Fatalf("runSnapshot must still succeed under skip build: %v", err)
	}

	if fi, err := os.Stat(a.LedgerPath); err != nil || fi.Size() == 0 {
		t.Fatalf("ledger %s: want non-empty, got size err=%v", a.LedgerPath, err)
	}

	reconciliationBytes, err := os.ReadFile(a.ReconciliationPath)
	if err != nil {
		t.Fatalf("read reconciliation: %v", err)
	}

	var reconciliation report.Reconciliation
	if err := json.Unmarshal(reconciliationBytes, &reconciliation); err != nil {
		t.Fatalf("unmarshal reconciliation.json: %v", err)
	}

	if reconciliation.Verified {
		t.Fatalf("reconciliation.json from an unverified archive must set verified=false:\n%s", reconciliationBytes)
	}
}
