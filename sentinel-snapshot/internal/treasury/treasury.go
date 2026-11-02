package treasury

import (
	"fmt"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type ChannelEscrowAmount struct {
	PortID    string         `json:"port_id"`
	ChannelID string         `json:"channel_id"`
	Address   sdk.AccAddress `json:"address"`
	Amount    math.Int       `json:"amount"`
}

type Components struct {
	DirectlyMeasured         math.Int
	DistributionSweep        math.Int
	StakingDust              math.Int
	PermanentLockedPrincipal math.Int
	IBCFeeEscrow             math.Int
	NonLeafAttributedEscrow  math.Int
	IBCEscrowByChannel       []ChannelEscrowAmount
}

func (c Components) Total() math.Int {
	return c.DirectlyMeasured.Add(c.DistributionSweep).Add(c.StakingDust).Add(c.PermanentLockedPrincipal)
}

type CreditedTotals struct {
	Rewards    math.Int
	Commission math.Int
}

func ValidateClassified(unclassified math.Int) error {
	if !unclassified.IsZero() {
		return fmt.Errorf("%s unclassified module-account balance remains (must be leafed or swept)", unclassified)
	}

	return nil
}

func DistributionSweep(moduleBalance, creditedRewards, creditedCommission math.Int) math.Int {
	return moduleBalance.Sub(creditedRewards).Sub(creditedCommission)
}

func Reconcile(supplyAtHalt, totalClaimable, treasuryTotal math.Int) error {
	got := totalClaimable.Add(treasuryTotal)
	if !got.Equal(supplyAtHalt) {
		return fmt.Errorf("reconciliation failed: Σleaves(%s) + treasury(%s) = %s != supply at halt(%s)",
			totalClaimable, treasuryTotal, got, supplyAtHalt)
	}

	return nil
}
