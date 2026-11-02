package assemble

import (
	"fmt"

	"cosmossdk.io/math"
)

type Entitlement struct {
	LiquidUDVPN math.Int
	LockedUDVPN math.Int
}

func ComputeEntitlement(holdings Holdings, unvestedAtHalt math.Int) (Entitlement, error) {
	total := holdings.Balance.Add(holdings.Delegated).Add(holdings.Unbonding).
		Add(holdings.VPNDeposit).Add(holdings.GovDeposit).Add(holdings.Rewards).Add(holdings.Commission)
	alwaysLiquid := holdings.Rewards.Add(holdings.Commission).Add(holdings.VPNDeposit).Add(holdings.GovDeposit)

	lockCap := total.Sub(alwaysLiquid)
	if lockCap.IsNegative() {
		return Entitlement{}, fmt.Errorf(
			"total(%s) − always_liquid(%s) is negative; holdings are inconsistent",
			total, alwaysLiquid)
	}

	locked := math.MinInt(unvestedAtHalt, lockCap)
	liquid := total.Sub(locked)

	return Entitlement{
		LiquidUDVPN: liquid,
		LockedUDVPN: locked,
	}, nil
}

func IsLeafEligible(entitlement Entitlement) bool {
	return !entitlement.LiquidUDVPN.IsZero() || !entitlement.LockedUDVPN.IsZero()
}

type Holdings struct {
	Balance    math.Int
	Delegated  math.Int
	Unbonding  math.Int
	VPNDeposit math.Int
	GovDeposit math.Int
	Rewards    math.Int
	Commission math.Int
}

func NewHoldings() Holdings {
	zero := math.ZeroInt()

	return Holdings{
		Balance:    zero,
		Delegated:  zero,
		Unbonding:  zero,
		VPNDeposit: zero,
		GovDeposit: zero,
		Rewards:    zero,
		Commission: zero,
	}
}

func IsRecordEligible(liquid, locked uint64) bool {
	return liquid != 0 || locked != 0
}
