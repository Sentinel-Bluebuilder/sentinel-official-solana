//go:build integration

package bank

import (
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/app"
)

const DenomUDVPN = "udvpn"

func BalanceUDVPN(h *app.Handle, address sdk.AccAddress) math.Int {
	return h.App().BankKeeper.GetBalance(h.Context(), address, DenomUDVPN).Amount
}

func BalancesByAddress(h *app.Handle) map[string]math.Int {
	balances := make(map[string]math.Int)

	h.App().BankKeeper.IterateAllBalances(h.Context(), func(address sdk.AccAddress, coin sdk.Coin) bool {
		if coin.Denom == DenomUDVPN {
			balances[address.String()] = coin.Amount
		}

		return false
	})

	return balances
}

func SupplyUDVPN(h *app.Handle) math.Int {
	return h.App().BankKeeper.GetSupply(h.Context(), DenomUDVPN).Amount
}
