//go:build integration

package gov

import (
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"

	"github.com/sentinel-official/sentinel-snapshot/internal/app"
	"github.com/sentinel-official/sentinel-snapshot/internal/bank"
)

func DepositByAddress(h *app.Handle) map[string]math.Int {
	out := make(map[string]math.Int)

	h.App().GovKeeper.IterateAllDeposits(h.Context(), func(deposit govv1.Deposit) bool {
		amount := sdk.Coins(deposit.Amount).AmountOf(bank.DenomUDVPN)
		if existing, ok := out[deposit.Depositor]; ok {
			out[deposit.Depositor] = existing.Add(amount)
		} else {
			out[deposit.Depositor] = amount
		}

		return false
	})

	return out
}
