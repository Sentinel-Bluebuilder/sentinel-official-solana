package strandcheck

import (
	"fmt"

	"github.com/sentinel-official/sentinel-snapshot/internal/ledger"
)

type StrandedError struct {
	AddressBech32 string
	Family        string
	LiquidUDVPN   uint64
	LockedUDVPN   uint64
}

func (e *StrandedError) Error() string {
	return fmt.Sprintf("stranded unrevealed leaf: %s is a known %s no-preimage address but was leafed "+
		"under the unrevealed-key placeholder with liquid=%d locked=%d — it would be permanently "+
		"unclaimable", e.AddressBech32, e.Family, e.LiquidUDVPN, e.LockedUDVPN)
}

func ValidateNoStrandedUnrevealed(records []ledger.Record, familyByAddress map[[20]byte]string) error {
	for _, r := range records {
		if r.PubKey != nil {
			continue
		}

		if family, ok := familyByAddress[r.Address]; ok {
			return &StrandedError{
				AddressBech32: r.AddressBech32,
				Family:        family,
				LiquidUDVPN:   r.LiquidUDVPN,
				LockedUDVPN:   r.LockedUDVPN,
			}
		}
	}

	return nil
}
