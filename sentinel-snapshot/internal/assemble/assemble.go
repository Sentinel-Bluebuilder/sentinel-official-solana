//go:build integration

package assemble

import (
	"context"
	"fmt"
	"log/slog"

	"cosmossdk.io/math"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	vestingexported "github.com/cosmos/cosmos-sdk/x/auth/vesting/exported"
	vestingtypes "github.com/cosmos/cosmos-sdk/x/auth/vesting/types"
	"golang.org/x/sync/errgroup"

	"github.com/sentinel-official/sentinel-snapshot/internal/app"
	"github.com/sentinel-official/sentinel-snapshot/internal/auth"
	"github.com/sentinel-official/sentinel-snapshot/internal/auth/vesting"
	"github.com/sentinel-official/sentinel-snapshot/internal/bank"
	"github.com/sentinel-official/sentinel-snapshot/internal/deposit"
	"github.com/sentinel-official/sentinel-snapshot/internal/distribution"
	"github.com/sentinel-official/sentinel-snapshot/internal/gov"
	"github.com/sentinel-official/sentinel-snapshot/internal/progress"
	"github.com/sentinel-official/sentinel-snapshot/internal/staking"
	"github.com/sentinel-official/sentinel-snapshot/internal/treasury"
)

const checkInterval = 4096

type Account struct {
	Address          [20]byte
	AddressBech32    string
	CompressedPubKey []byte
	LiquidUDVPN      uint64
	LockedUDVPN      uint64
	Schedule         *vesting.Schedule
}

type Result struct {
	Accounts                 []Account
	SupplyAtHalt             math.Int
	PermanentLockedPrincipal math.Int
	Credited                 treasury.CreditedTotals
	NonLeafAttributedEscrow  math.Int
	ClassifiedAccounts       []auth.Account
	BalanceByAddress         map[string]math.Int
	DelegatedByAddress       map[string]math.Int
	UnbondingByAddress       map[string]math.Int
}

//nolint:gocyclo,maintidx
func Run(ctx context.Context, h *app.Handle, escrowAddresses map[string]bool, workers int) (Result, error) {
	result := Result{
		SupplyAtHalt:             bank.SupplyUDVPN(h),
		PermanentLockedPrincipal: math.ZeroInt(),
		Credited: treasury.CreditedTotals{
			Rewards:    math.ZeroInt(),
			Commission: math.ZeroInt(),
		},
		NonLeafAttributedEscrow: math.ZeroInt(),
	}

	var (
		credited                                                 distribution.Credited
		vpnDepositByAddress, govDepositByAddress                 map[string]math.Int
		balanceByAddress, delegatedByAddress, unbondingByAddress map[string]math.Int
		classified                                               []auth.Account
		firstPass                                                errgroup.Group
	)
	firstPass.Go(func() error {
		workerHandle, err := h.WithNewContext()
		if err != nil {
			return err
		}

		credited, err = distribution.CreditedByAddress(workerHandle)

		return err
	})
	firstPass.Go(func() error {
		workerHandle, err := h.WithNewContext()
		if err != nil {
			return err
		}

		vpnDepositByAddress = deposit.ByAddress(workerHandle)

		return nil
	})
	firstPass.Go(func() error {
		workerHandle, err := h.WithNewContext()
		if err != nil {
			return err
		}

		govDepositByAddress = gov.DepositByAddress(workerHandle)

		return nil
	})
	firstPass.Go(func() error {
		workerHandle, err := h.WithNewContext()
		if err != nil {
			return err
		}

		balanceByAddress = bank.BalancesByAddress(workerHandle)

		return nil
	})
	firstPass.Go(func() error {
		workerHandle, err := h.WithNewContext()
		if err != nil {
			return err
		}

		delegatedByAddress = staking.DelegatedByAddress(workerHandle)

		return nil
	})
	firstPass.Go(func() error {
		workerHandle, err := h.WithNewContext()
		if err != nil {
			return err
		}

		unbondingByAddress = staking.UnbondingByAddress(workerHandle)

		return nil
	})
	firstPass.Go(func() error {
		workerHandle, err := h.WithNewContext()
		if err != nil {
			return err
		}

		classified, err = auth.ClassifyAccounts(ctx, workerHandle, escrowAddresses)

		return err
	})

	if err := firstPass.Wait(); err != nil {
		return Result{}, fmt.Errorf("assemble first pass: %w", err)
	}

	tracker := progress.New(ctx, slog.Default(), "assemble", int64(len(classified)))
	defer tracker.Stop()

	type workerTotals struct {
		accounts                []Account
		rewards                 math.Int
		commission              math.Int
		permanentLocked         math.Int
		nonLeafAttributedEscrow math.Int
	}

	workerCount := max(min(workers, len(classified)), 1)

	totals := make([]workerTotals, workerCount)
	for i := range totals {
		totals[i] = workerTotals{
			rewards:                 math.ZeroInt(),
			commission:              math.ZeroInt(),
			permanentLocked:         math.ZeroInt(),
			nonLeafAttributedEscrow: math.ZeroInt(),
		}
	}

	accountGroup, groupCtx := errgroup.WithContext(ctx)

	chunk := (len(classified) + workerCount - 1) / workerCount
	for workerIndex := range workerCount {
		lo := workerIndex * chunk

		hi := min(lo+chunk, len(classified))

		if lo >= hi {
			continue
		}

		accountGroup.Go(func() error {
			workerHandle, err := h.WithNewContext()
			if err != nil {
				return err
			}

			workerResult := workerTotals{
				rewards:                 math.ZeroInt(),
				commission:              math.ZeroInt(),
				permanentLocked:         math.ZeroInt(),
				nonLeafAttributedEscrow: math.ZeroInt(),
			}

			for i := lo; i < hi; i++ {
				if (i-lo)%checkInterval == 0 {
					if err := groupCtx.Err(); err != nil {
						return fmt.Errorf("assemble account walk cancelled: %w", err)
					}
				}

				tracker.Add(1)

				classifiedAccount := classified[i]

				addressBech32 := classifiedAccount.AddressBech32

				leaf := classifiedAccount.Class == auth.ClassLeafRevealed ||
					classifiedAccount.Class == auth.ClassLeafUnrevealed
				if !leaf {
					if amount, ok := vpnDepositByAddress[addressBech32]; ok {
						workerResult.nonLeafAttributedEscrow = workerResult.nonLeafAttributedEscrow.Add(amount)
					}

					if amount, ok := govDepositByAddress[addressBech32]; ok {
						workerResult.nonLeafAttributedEscrow = workerResult.nonLeafAttributedEscrow.Add(amount)
					}

					continue
				}

				holdings := NewHoldings()
				if amount, ok := balanceByAddress[addressBech32]; ok {
					holdings.Balance = amount
				}

				if amount, ok := delegatedByAddress[addressBech32]; ok {
					holdings.Delegated = amount
				}

				if amount, ok := unbondingByAddress[addressBech32]; ok {
					holdings.Unbonding = amount
				}

				if amount, ok := vpnDepositByAddress[addressBech32]; ok {
					holdings.VPNDeposit = amount
				}

				if amount, ok := govDepositByAddress[addressBech32]; ok {
					holdings.GovDeposit = amount
				}

				if amount, ok := credited.Rewards[addressBech32]; ok {
					holdings.Rewards = amount
				}

				if amount, ok := credited.Commission[addressBech32]; ok {
					holdings.Commission = amount
				}

				workerResult.rewards = workerResult.rewards.Add(holdings.Rewards)
				workerResult.commission = workerResult.commission.Add(holdings.Commission)

				unvested, isPermanent, original, err := unvestedAtHalt(workerHandle, classifiedAccount.Address)
				if err != nil {
					return fmt.Errorf("%s: %w", addressBech32, err)
				}

				entitlement, err := ComputeEntitlement(holdings, unvested)
				if err != nil {
					return fmt.Errorf("%s: %w", addressBech32, err)
				}

				if !IsLeafEligible(entitlement) {
					continue
				}

				if len(classifiedAccount.Address) != auth.LeafAddressBytes {
					return fmt.Errorf("%s: address is %d bytes, want %d (auth.ClassifyAccount should have routed this "+
						"to Treasury)", addressBech32, len(classifiedAccount.Address), auth.LeafAddressBytes)
				}

				var addressBytes [auth.LeafAddressBytes]byte
				copy(addressBytes[:], classifiedAccount.Address)
				account := Account{
					Address:          addressBytes,
					AddressBech32:    addressBech32,
					CompressedPubKey: classifiedAccount.CompressedPubKey,
					LiquidUDVPN:      entitlement.LiquidUDVPN.Uint64(),
				}

				switch {
				case isPermanent:
					workerResult.permanentLocked = workerResult.permanentLocked.Add(entitlement.LockedUDVPN)
					account.LockedUDVPN = 0
					account.Schedule = nil
				case entitlement.LockedUDVPN.IsPositive():
					locked := entitlement.LockedUDVPN.Uint64()

					schedule, hasSchedule := vesting.Reanchor(original, locked, workerHandle.BlockTimeUnix())
					if hasSchedule {
						if err := schedule.Validate(locked); err != nil {
							return fmt.Errorf("%s: %w", account.AddressBech32, err)
						}

						account.LockedUDVPN = schedule.TotalUDVPN
						account.Schedule = &schedule
					} else {
						account.LiquidUDVPN += locked
					}
				}

				if !IsRecordEligible(account.LiquidUDVPN, account.LockedUDVPN) {
					continue
				}

				workerResult.accounts = append(workerResult.accounts, account)
			}

			totals[workerIndex] = workerResult

			return nil
		})
	}

	if err := accountGroup.Wait(); err != nil {
		return Result{}, fmt.Errorf("assemble account walk: %w", err)
	}

	for _, workerResult := range totals {
		result.Accounts = append(result.Accounts, workerResult.accounts...)
		result.Credited.Rewards = result.Credited.Rewards.Add(workerResult.rewards)
		result.Credited.Commission = result.Credited.Commission.Add(workerResult.commission)
		result.PermanentLockedPrincipal = result.PermanentLockedPrincipal.Add(workerResult.permanentLocked)
		result.NonLeafAttributedEscrow = result.NonLeafAttributedEscrow.Add(workerResult.nonLeafAttributedEscrow)
	}

	result.ClassifiedAccounts = classified
	result.BalanceByAddress = balanceByAddress
	result.DelegatedByAddress = delegatedByAddress
	result.UnbondingByAddress = unbondingByAddress

	return result, nil
}

func unvestedAtHalt(h *app.Handle, address sdk.AccAddress) (
	unvested math.Int,
	isPermanent bool,
	original vesting.Original,
	err error,
) {
	account := h.App().AccountKeeper.GetAccount(h.Context(), address)

	vestingAccount, ok := account.(vestingexported.VestingAccount)
	if !ok {
		return math.ZeroInt(), false, vesting.Original{}, nil
	}

	packed, err := codectypes.NewAnyWithValue(account)
	if err != nil {
		return math.Int{}, false, vesting.Original{}, fmt.Errorf("pack vesting account: %w", err)
	}

	kind, isPermanent, err := vesting.KindFromTypeURL(packed.TypeUrl)
	if err != nil {
		return math.Int{}, false, vesting.Original{}, err
	}

	vestingUDVPN := vestingAccount.GetOriginalVesting().AmountOf(bank.DenomUDVPN)
	if isPermanent {
		return vestingUDVPN, true, vesting.Original{}, nil
	}

	original = vesting.Original{
		Kind:       kind,
		StartUnix:  vestingAccount.GetStartTime(),
		EndUnix:    vestingAccount.GetEndTime(),
		TotalUDVPN: vestingUDVPN.Uint64(),
	}
	if periodicAccount, ok := account.(*vestingtypes.PeriodicVestingAccount); ok {
		original.Periods = make([]vesting.Period, 0, len(periodicAccount.VestingPeriods))
		for _, period := range periodicAccount.VestingPeriods {
			original.Periods = append(original.Periods, vesting.Period{
				LengthSeconds: period.Length,
				AmountUDVPN:   period.Amount.AmountOf(bank.DenomUDVPN).Uint64(),
			})
		}
	}

	total := vestingUDVPN.Uint64()

	unlocked := original.ToSchedule().UnlockedAt(h.BlockTimeUnix())
	if unlocked > total {
		return math.Int{}, false, vesting.Original{}, fmt.Errorf("unlocked %d exceeds vesting total %d", unlocked, total)
	}

	return math.NewIntFromUint64(total - unlocked), false, original, nil
}
