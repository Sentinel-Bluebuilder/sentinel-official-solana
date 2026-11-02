package treasury_test

import (
	"testing"

	"github.com/sentinel-official/sentinel-snapshot/internal/treasury"
)

func TestReconcile(t *testing.T) {
	t.Parallel()

	if err := treasury.Reconcile(newInt(1000), newInt(800), newInt(200)); err != nil {
		t.Fatalf("balanced identity should pass: %v", err)
	}

	if err := treasury.Reconcile(newInt(1000), newInt(800), newInt(199)); err == nil {
		t.Fatal("a deflated ledger must fail the reconciliation gate")
	}

	if err := treasury.Reconcile(newInt(1000), newInt(801), newInt(200)); err == nil {
		t.Fatal("an inflated ledger must fail the reconciliation gate")
	}
}
