//go:build integration

package deposit

import (
	"cosmossdk.io/math"

	"github.com/sentinel-official/sentinel-snapshot/internal/app"
	"github.com/sentinel-official/sentinel-snapshot/internal/bank"
	depositv1 "github.com/sentinel-official/sentinelhub/v12/x/deposit/types/v1"
)

func ByAddress(h *app.Handle) map[string]math.Int {
	out := make(map[string]math.Int)

	h.App().VPNKeeper.Deposit.IterateDeposits(h.Context(), func(_ int, deposit depositv1.Deposit) bool {
		out[deposit.Address] = deposit.Coins.AmountOf(bank.DenomUDVPN)

		return false
	})

	return out
}
