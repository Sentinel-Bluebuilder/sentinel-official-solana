//go:build integration && unsafeskipverify

package app_test

import (
	"testing"

	"github.com/sentinel-official/sentinel-snapshot/internal/app"
)

func TestVerifyArchiveGate_UnsafeBuildSkips(t *testing.T) {
	t.Parallel()

	verified, err := app.VerifyArchiveGate(t.Context(), nil, 0, nil, nil, 1)
	if err != nil {
		t.Fatalf("unsafe gate must not error, got %v", err)
	}

	if verified {
		t.Fatal("unsafe gate must report verified=false (rehash skipped)")
	}
}
