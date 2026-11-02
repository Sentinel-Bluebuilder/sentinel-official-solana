package assemble_test

import (
	"testing"

	"cosmossdk.io/math"

	"github.com/sentinel-official/sentinel-snapshot/internal/assemble"
)

func newInt(value int64) math.Int { return math.NewInt(value) }

func mustComputeEntitlement(t *testing.T, holdings assemble.Holdings, unvested math.Int) assemble.Entitlement {
	t.Helper()

	entitlement, err := assemble.ComputeEntitlement(holdings, unvested)
	if err != nil {
		t.Fatalf("ComputeEntitlement: %v", err)
	}

	return entitlement
}
