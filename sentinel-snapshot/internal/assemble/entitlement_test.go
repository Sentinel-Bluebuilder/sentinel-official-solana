package assemble_test

import (
	"testing"

	"github.com/sentinel-official/sentinel-snapshot/internal/assemble"
)

func TestComputeEntitlement_SlashedDelegatedVesting(t *testing.T) {
	t.Parallel()

	holdings := assemble.Holdings{
		Balance:    newInt(0),
		Delegated:  newInt(300),
		Unbonding:  newInt(0),
		VPNDeposit: newInt(0),
		GovDeposit: newInt(0),
		Rewards:    newInt(50),
		Commission: newInt(0),
	}

	entitlement := mustComputeEntitlement(t, holdings, newInt(600))
	if !entitlement.LockedUDVPN.Equal(newInt(300)) {
		t.Fatalf("locked = %s, want 300 (min guard clamps to cap)", entitlement.LockedUDVPN)
	}

	if !entitlement.LiquidUDVPN.Equal(newInt(50)) {
		t.Fatalf("liquid = %s, want 50 (rewards stay liquid)", entitlement.LiquidUDVPN)
	}
}

func TestIsRecordEligible_PermanentLockedNoLiquid(t *testing.T) {
	t.Parallel()

	holdings := assemble.Holdings{
		Balance:    newInt(1000),
		Delegated:  newInt(0),
		Unbonding:  newInt(0),
		VPNDeposit: newInt(0),
		GovDeposit: newInt(0),
		Rewards:    newInt(0),
		Commission: newInt(0),
	}

	entitlement := mustComputeEntitlement(t, holdings, newInt(1000))
	if !entitlement.LockedUDVPN.Equal(newInt(1000)) || !entitlement.LiquidUDVPN.IsZero() {
		t.Fatalf("locked=%s liquid=%s, want 1000/0", entitlement.LockedUDVPN, entitlement.LiquidUDVPN)
	}

	if !assemble.IsLeafEligible(entitlement) {
		t.Fatal("ComputeEntitlement result is positive, so the account is leaf-eligible here")
	}

	if assemble.IsRecordEligible(0, 0) {
		t.Fatal("a fully-swept PermanentLocked account must not reach the ledger")
	}

	if !assemble.IsRecordEligible(250, 0) {
		t.Fatal("post-creation transfer excess must still be leafed")
	}
}
