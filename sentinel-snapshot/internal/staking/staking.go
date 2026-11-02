//go:build integration

package staking

import (
	"fmt"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/app"
)

const maxDelegationsPerQuery = 65535

func DelegatedByAddress(h *app.Handle) map[string]math.Int {
	validatorsByOperator := make(map[string]stakingtypes.ValidatorI)

	h.App().StakingKeeper.IterateValidators(h.Context(), func(_ int64, validator stakingtypes.ValidatorI) bool {
		validatorsByOperator[validator.GetOperator().String()] = validator

		return false
	})

	delegated := make(map[string]math.Int)

	h.App().StakingKeeper.IterateAllDelegations(h.Context(), func(delegation stakingtypes.Delegation) bool {
		validator, ok := validatorsByOperator[delegation.ValidatorAddress]
		if !ok {
			return false
		}

		tokens := TokensFromShares(delegation.Shares, validator.GetTokens(), validator.GetDelegatorShares())
		if existing, ok := delegated[delegation.DelegatorAddress]; ok {
			delegated[delegation.DelegatorAddress] = existing.Add(tokens)
		} else {
			delegated[delegation.DelegatorAddress] = tokens
		}

		return false
	})

	return delegated
}

func DelegatedTokens(h *app.Handle, address sdk.AccAddress) (math.Int, error) {
	delegations := h.App().StakingKeeper.GetDelegatorDelegations(h.Context(), address, uint16(maxDelegationsPerQuery))
	if len(delegations) == maxDelegationsPerQuery {
		return math.Int{}, fmt.Errorf("staking: delegator %s has exactly maxDelegationsPerQuery (%d) delegations; "+
			"cannot tell whether GetDelegatorDelegations truncated", address, maxDelegationsPerQuery)
	}

	total := math.ZeroInt()

	for _, delegation := range delegations {
		operator, err := sdk.ValAddressFromBech32(delegation.ValidatorAddress)
		if err != nil {
			return math.Int{}, fmt.Errorf(
				"staking: delegation %q has an invalid validator address %q: %w",
				address,
				delegation.ValidatorAddress,
				err,
			)
		}

		validator, found := h.App().StakingKeeper.GetValidator(h.Context(), operator)
		if !found {
			return math.Int{}, fmt.Errorf(
				"staking: delegation %q references validator %q, which does not exist",
				address,
				delegation.ValidatorAddress,
			)
		}

		total = total.Add(TokensFromShares(delegation.Shares, validator.Tokens, validator.DelegatorShares))
	}

	return total, nil
}

func UnbondingTokens(h *app.Handle, address sdk.AccAddress) math.Int {
	total := math.ZeroInt()

	for _, ubd := range h.App().StakingKeeper.GetAllUnbondingDelegations(h.Context(), address) {
		for _, entry := range ubd.Entries {
			total = total.Add(entry.Balance)
		}
	}

	return total
}

func UnbondingByAddress(h *app.Handle) map[string]math.Int {
	unbonding := make(map[string]math.Int)

	h.App().StakingKeeper.IterateUnbondingDelegations(h.Context(), func(
		_ int64,
		ubd stakingtypes.UnbondingDelegation,
	) bool {
		total := math.ZeroInt()
		for _, entry := range ubd.Entries {
			total = total.Add(entry.Balance)
		}

		if existing, ok := unbonding[ubd.DelegatorAddress]; ok {
			unbonding[ubd.DelegatorAddress] = existing.Add(total)
		} else {
			unbonding[ubd.DelegatorAddress] = total
		}

		return false
	})

	return unbonding
}
