package report

import (
	"encoding/json"
	"fmt"

	"cosmossdk.io/math"

	"github.com/sentinel-official/sentinel-snapshot/internal/treasury"
)

type Reconciliation struct {
	SupplyAtHalt            string                         `json:"supply_at_halt"`
	TotalClaimable          string                         `json:"total_claimable"`
	TreasuryTotal           string                         `json:"treasury_total"`
	IBCFeeEscrow            string                         `json:"ibc_fee_escrow"`
	NonLeafAttributedEscrow string                         `json:"non_leaf_attributed_escrow"`
	LedgerContentHash       string                         `json:"ledger_content_hash"`
	IBCEscrowByChannel      []treasury.ChannelEscrowAmount `json:"ibc_escrow_by_channel"`
	IBCEscrowTotal          string                         `json:"ibc_escrow_total"`
	Verified                bool                           `json:"verified"`
}

func (r Reconciliation) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal reconciliation: %w", err)
	}

	return b, nil
}

func New(
	supplyAtHalt math.Int,
	totalClaimable math.Int,
	treasuryTotal math.Int,
	ledgerContentHashHex string,
	ibcFeeEscrow math.Int,
	nonLeafAttributedEscrow math.Int,
	ibcEscrows []treasury.ChannelEscrowAmount,
	verified bool,
) Reconciliation {
	escrowTotal := math.ZeroInt()
	for _, ibcEscrow := range ibcEscrows {
		escrowTotal = escrowTotal.Add(ibcEscrow.Amount)
	}

	return Reconciliation{
		SupplyAtHalt:            supplyAtHalt.String(),
		TotalClaimable:          totalClaimable.String(),
		TreasuryTotal:           treasuryTotal.String(),
		IBCFeeEscrow:            ibcFeeEscrow.String(),
		NonLeafAttributedEscrow: nonLeafAttributedEscrow.String(),
		LedgerContentHash:       ledgerContentHashHex,
		IBCEscrowByChannel:      ibcEscrows,
		IBCEscrowTotal:          escrowTotal.String(),
		Verified:                verified,
	}
}
