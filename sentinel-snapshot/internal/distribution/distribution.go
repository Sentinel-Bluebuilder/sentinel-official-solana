//go:build integration

package distribution

import (
	"fmt"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/app"
	"github.com/sentinel-official/sentinel-snapshot/internal/bank"
)

type Credited struct {
	Rewards    map[string]math.Int
	Commission map[string]math.Int
}

func CreditedByAddress(h *app.Handle) (Credited, error) {
	credited := Credited{
		Rewards:    make(map[string]math.Int),
		Commission: make(map[string]math.Int),
	}

	delegationsByValidator := make(map[string][]stakingtypes.Delegation)

	h.App().StakingKeeper.IterateAllDelegations(h.Context(), func(delegation stakingtypes.Delegation) bool {
		delegationsByValidator[delegation.ValidatorAddress] = append(
			delegationsByValidator[delegation.ValidatorAddress],
			delegation,
		)

		return false
	})

	var walkErr error

	h.App().StakingKeeper.IterateValidators(h.Context(), func(_ int64, validator stakingtypes.ValidatorI) bool {
		operator := validator.GetOperator()
		outstanding := ValidatorOutstandingRewards(h, operator)
		commission := ValidatorCommission(h, operator)

		delegatorRewards, err := delegatorRewardsForDelegations(h, operator, delegationsByValidator[operator.String()])
		if err != nil {
			walkErr = err

			return true
		}

		rewards, commissionCredited := ClampToOutstanding(outstanding, commission, delegatorRewards)

		for address, amount := range rewards {
			if existing, ok := credited.Rewards[address]; ok {
				credited.Rewards[address] = existing.Add(amount)
			} else {
				credited.Rewards[address] = amount
			}
		}

		operatorAccount := sdk.AccAddress(operator).String()
		if existing, ok := credited.Commission[operatorAccount]; ok {
			credited.Commission[operatorAccount] = existing.Add(commissionCredited)
		} else {
			credited.Commission[operatorAccount] = commissionCredited
		}

		return false
	})

	if walkErr != nil {
		return Credited{}, walkErr
	}

	return credited, nil
}

func DelegatorRewards(h *app.Handle, operator sdk.ValAddress) ([]Credit, error) {
	delegations := h.App().StakingKeeper.GetValidatorDelegations(h.Context(), operator)

	return delegatorRewardsForDelegations(h, operator, delegations)
}

func ValidatorCommission(h *app.Handle, operator sdk.ValAddress) math.LegacyDec {
	accumulated := h.App().DistributionKeeper.GetValidatorAccumulatedCommission(h.Context(), operator)

	return accumulated.Commission.AmountOf(bank.DenomUDVPN)
}

func ValidatorOutstandingRewards(h *app.Handle, operator sdk.ValAddress) math.LegacyDec {
	outstanding := h.App().DistributionKeeper.GetValidatorOutstandingRewards(h.Context(), operator)

	return outstanding.Rewards.AmountOf(bank.DenomUDVPN)
}

func delegatorRewardsForDelegations(
	h *app.Handle,
	operator sdk.ValAddress,
	delegations []stakingtypes.Delegation,
) ([]Credit, error) {
	validator, found := h.App().StakingKeeper.GetValidator(h.Context(), operator)
	if !found {
		return nil, fmt.Errorf("distribution: validator %s not found", operator)
	}

	credits := make([]Credit, 0, len(delegations))
	for _, delegation := range delegations {
		delegatorAddress, err := sdk.AccAddressFromBech32(delegation.DelegatorAddress)
		if err != nil {
			return nil, fmt.Errorf(
				"distribution: delegation has invalid delegator address %q: %w",
				delegation.DelegatorAddress,
				err,
			)
		}

		endingPeriod := h.App().DistributionKeeper.IncrementValidatorPeriod(h.Context(), validator)
		rewards := h.App().DistributionKeeper.CalculateDelegationRewards(h.Context(), validator, delegation, endingPeriod)
		credits = append(credits, Credit{
			Address: delegatorAddress,
			Amount:  rewards.AmountOf(bank.DenomUDVPN),
		})
	}

	return credits, nil
}
