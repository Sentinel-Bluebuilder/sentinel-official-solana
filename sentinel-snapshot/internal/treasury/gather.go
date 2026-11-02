//go:build integration

package treasury

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/math"
	wasmtypes "github.com/CosmWasm/wasmd/x/wasm/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	distributiontypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	ibcicatypes "github.com/cosmos/ibc-go/v7/modules/apps/27-interchain-accounts/types"
	ibcfeetypes "github.com/cosmos/ibc-go/v7/modules/apps/29-fee/types"
	ibctransfertypes "github.com/cosmos/ibc-go/v7/modules/apps/transfer/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/app"
	"github.com/sentinel-official/sentinel-snapshot/internal/auth"
	"github.com/sentinel-official/sentinel-snapshot/internal/bank"
	"github.com/sentinel-official/sentinel-snapshot/internal/ibcescrow"
	"github.com/sentinel-official/sentinel-snapshot/internal/staking"
	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
	deposittypes "github.com/sentinel-official/sentinelhub/v12/x/deposit/types"
)

const checkInterval = 4096

//nolint:gochecknoglobals
var (
	attributedModuleNames = map[string]bool{
		stakingtypes.BondedPoolName:    true,
		stakingtypes.NotBondedPoolName: true,
		distributiontypes.ModuleName:   true,
		govtypes.ModuleName:            true,
		deposittypes.ModuleName:        true,
	}
	unattributedModuleNames = map[string]bool{
		authtypes.FeeCollectorName:  true,
		ibctransfertypes.ModuleName: true,
		ibcicatypes.ModuleName:      true,
		wasmtypes.ModuleName:        true,
	}
)

type GatherInput struct {
	Credited                 CreditedTotals
	PermanentLockedPrincipal math.Int
	NonLeafAttributedEscrow  math.Int
	Escrows                  []ibcescrow.Channel
	ClassifiedAccounts       []auth.Account
	BalanceByAddress         map[string]math.Int
	DelegatedByAddress       map[string]math.Int
	UnbondingByAddress       map[string]math.Int
}

func Gather(ctx context.Context, h *app.Handle, input GatherInput) (Components, error) {
	directlyMeasured := math.ZeroInt().Add(input.NonLeafAttributedEscrow)
	unclassified := math.ZeroInt()
	ibcFeeEscrow := math.ZeroInt()

	var distributionModuleBalance math.Int

	for name := range sentinelapp.ModuleAccPerms() {
		address := authtypes.NewModuleAddress(name)
		balance := bank.BalanceUDVPN(h, address)

		switch {
		case name == ibcfeetypes.ModuleName:
			if !balance.IsZero() {
				return Components{}, fmt.Errorf(
					"treasury: ibcfee (ICS-29) module account %s holds a nonzero balance of %s udvpn — "+
						"this is user-attributable per-packet fee escrow, not a treasury sweep; "+
						"an explicit owner disposition is required before this snapshot may proceed "+
						"(see VERSIONS.md's Task-7 note)", address, balance)
			}

			ibcFeeEscrow = balance
		case attributedModuleNames[name]:
			if name == distributiontypes.ModuleName {
				distributionModuleBalance = balance
			}
		case unattributedModuleNames[name]:
			directlyMeasured = directlyMeasured.Add(balance)
		default:
			unclassified = unclassified.Add(balance)
		}
	}

	if distributionModuleBalance.IsNil() {
		return Components{}, errors.New("treasury: distribution module balance was never read")
	}

	moduleAddresses := make(map[string]bool)
	for name := range sentinelapp.ModuleAccPerms() {
		moduleAddresses[authtypes.NewModuleAddress(name).String()] = true
	}

	escrowAddresses := make(map[string]bool, len(input.Escrows))
	for _, escrow := range input.Escrows {
		escrowAddresses[escrow.Address.String()] = true
	}

	for i, account := range input.ClassifiedAccounts {
		if i%checkInterval == 0 {
			if err := ctx.Err(); err != nil {
				return Components{}, fmt.Errorf("gather cancelled: %w", err)
			}
		}

		if account.Class != auth.ClassTreasury || moduleAddresses[account.AddressBech32] ||
			escrowAddresses[account.AddressBech32] {
			continue
		}

		directlyMeasured = directlyMeasured.
			Add(intOrZero(input.BalanceByAddress, account.AddressBech32)).
			Add(intOrZero(input.DelegatedByAddress, account.AddressBech32)).
			Add(intOrZero(input.UnbondingByAddress, account.AddressBech32))
	}

	escrowByChannel := make([]ChannelEscrowAmount, 0, len(input.Escrows))
	for _, escrow := range input.Escrows {
		delegated, err := staking.DelegatedTokens(h, escrow.Address)
		if err != nil {
			return Components{}, fmt.Errorf("escrow %s/%s: %w", escrow.PortID, escrow.ChannelID, err)
		}

		amount := bank.BalanceUDVPN(h, escrow.Address).Add(delegated).Add(staking.UnbondingTokens(h, escrow.Address))
		directlyMeasured = directlyMeasured.Add(amount)
		escrowByChannel = append(escrowByChannel, ChannelEscrowAmount{
			PortID:    escrow.PortID,
			ChannelID: escrow.ChannelID,
			Address:   escrow.Address,
			Amount:    amount,
		})
	}

	distributionSweep := DistributionSweep(
		distributionModuleBalance,
		input.Credited.Rewards,
		input.Credited.Commission,
	)

	stakingDust := math.ZeroInt()

	var walkErr error

	h.App().StakingKeeper.IterateValidators(h.Context(), func(_ int64, validator stakingtypes.ValidatorI) bool {
		delegations := h.App().StakingKeeper.GetValidatorDelegations(h.Context(), validator.GetOperator())

		tokens := make([]math.Int, 0, len(delegations))
		for _, delegation := range delegations {
			tokens = append(tokens, staking.TokensFromShares(
				delegation.Shares,
				validator.GetTokens(),
				validator.GetDelegatorShares(),
			))
		}

		dust, err := staking.ValidatorPoolDust(validator.GetTokens(), tokens)
		if err != nil {
			walkErr = fmt.Errorf("validator %s: %w", validator.GetOperator(), err)

			return true
		}

		stakingDust = stakingDust.Add(dust)

		return false
	})

	if walkErr != nil {
		return Components{}, walkErr
	}

	components := Components{
		DirectlyMeasured:         directlyMeasured,
		DistributionSweep:        distributionSweep,
		StakingDust:              stakingDust,
		PermanentLockedPrincipal: input.PermanentLockedPrincipal,
		IBCFeeEscrow:             ibcFeeEscrow,

		NonLeafAttributedEscrow: input.NonLeafAttributedEscrow,

		IBCEscrowByChannel: escrowByChannel,
	}

	if err := ValidateClassified(unclassified); err != nil {
		return Components{}, err
	}

	return components, nil
}

func intOrZero(amounts map[string]math.Int, key string) math.Int {
	if amount, ok := amounts[key]; ok {
		return amount
	}

	return math.ZeroInt()
}
