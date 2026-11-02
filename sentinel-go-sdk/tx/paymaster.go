package tx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/solana-foundation/solana-go/v2/rpc/ws"
)

var (
	ErrFeeTransferInstructionsMismatch = errors.New("fee transfer instruction count mismatch")
	ErrPaymasterMessageChanged         = errors.New("paymaster message changed")
	ErrPaymasterSignatureMismatch      = errors.New("paymaster signature mismatch")
)

var (
	errPaymasterFailed = errors.New("paymaster failed")
	errUnknownSigner   = errors.New("unknown signer")
)

type FeeTransfer interface {
	Instructions() []solana.Instruction
	Quote(ctx context.Context, t *solana.Transaction) (TokenFee, []solana.Instruction, error)
}

type Paymaster interface {
	Address(ctx context.Context) (solana.PublicKey, error)
	FallsBack() bool
	FeeTransfer(ctx context.Context, signer solana.PublicKey) (FeeTransfer, error)
	Sign(ctx context.Context, t *solana.Transaction) (*solana.Transaction, error)
}

type TokenFee struct {
	BaseUnits uint64           `json:"base_units"`
	FeeToken  solana.PublicKey `json:"fee_token"`
}

type selfPaidInstruction struct {
	solana.Instruction

	accounts []*solana.AccountMeta
}

func (s selfPaidInstruction) Accounts() []*solana.AccountMeta {
	return s.accounts
}

func blamesPaymaster(txErr any, probe *solana.Transaction, firstFeeTransfer int, rentPayer solana.PublicKey) bool {
	switch txErr {
	case "AccountNotFound", "InsufficientFundsForFee", "InvalidAccountForFee":
		return true
	}

	data, err := json.Marshal(txErr)
	if err != nil {
		return false
	}

	var failure map[string]json.RawMessage
	if json.Unmarshal(data, &failure) != nil {
		return false
	}

	var rent struct {
		AccountIndex int `json:"account_index"`
	}
	if json.Unmarshal(failure["InsufficientFundsForRent"], &rent) == nil {
		return rent.AccountIndex == 0
	}

	var (
		index       int
		instruction []json.RawMessage
	)

	if json.Unmarshal(failure["InstructionError"], &instruction) != nil || len(instruction) != 2 ||
		json.Unmarshal(instruction[0], &index) != nil || index < 0 || index >= len(probe.Message.Instructions) {
		return false
	}

	return index >= firstFeeTransfer || signedBy(probe, index, rentPayer)
}

func buildWithFeeTransfer(
	ctx context.Context,
	buildWith func([]solana.Instruction) (*solana.Transaction, error),
	ixs []solana.Instruction,
	feeTransfer FeeTransfer,
	placeholders int,
) (*solana.Transaction, *TokenFee, error) {
	t, err := buildWith(ixs)
	if err != nil {
		return nil, nil, err
	}

	if feeTransfer == nil {
		return t, nil, nil
	}

	tokenFee, quoted, err := feeTransfer.Quote(ctx, t)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: quoting fee transfer: %w", errPaymasterFailed, err)
	}

	if len(quoted) != placeholders {
		return nil, nil, fmt.Errorf(
			"%w: %w: quoted %d for %d placeholders",
			errPaymasterFailed,
			ErrFeeTransferInstructionsMismatch,
			len(quoted),
			placeholders,
		)
	}

	t, err = buildWith(slices.Concat(ixs[:len(ixs)-placeholders], quoted))
	if err != nil {
		return nil, nil, err
	}

	return t, &tokenFee, nil
}

func markOversizeFailed(paymaster Paymaster, err error) error {
	if !errors.Is(err, ErrTransactionTooLarge) {
		return err
	}

	return markPaymasterFailed(paymaster, err)
}

func markPaymasterFailed(paymaster Paymaster, err error) error {
	if paymaster == nil || err == nil {
		return err
	}

	return fmt.Errorf("%w: %w", errPaymasterFailed, err)
}

func resolveFeeTransfer(
	ctx context.Context,
	paymaster Paymaster,
	signer solana.PublicKey,
) (FeeTransfer, []solana.Instruction, error) {
	if paymaster == nil {
		return nil, nil, nil
	}

	feeTransfer, err := paymaster.FeeTransfer(ctx, signer)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: fetching fee transfer: %w", errPaymasterFailed, err)
	}

	if feeTransfer == nil {
		return nil, nil, nil
	}

	return feeTransfer, feeTransfer.Instructions(), nil
}

func resolvePayer(ctx context.Context, paymaster Paymaster, signer solana.PublicKey) (solana.PublicKey, error) {
	if paymaster == nil {
		return signer, nil
	}

	address, err := paymaster.Address(ctx)
	if err != nil {
		return solana.PublicKey{}, fmt.Errorf("%w: fetching address: %w", errPaymasterFailed, err)
	}

	return address, nil
}

func sendSelfPaid(
	ctx context.Context,
	rpcClient *rpc.Client,
	wsClient *ws.Client,
	signer solana.PrivateKey,
	cfg Config,
	ixs []solana.Instruction,
	opts options,
	paymasterErr error,
) (*Receipt, bool, error) {
	selfPaid, err := toSelfPaid(ixs, append([]solana.PrivateKey{signer}, opts.signers...), opts.rentPayer)
	if err != nil {
		opts.log.Warn().Err(err).Msg("not falling back to the signer")

		return nil, false, fmt.Errorf("not falling back: %w: %w", err, paymasterErr)
	}

	opts.log.Warn().Err(paymasterErr).Msg("paying fees and rent with the signer")
	opts.paymaster = nil

	receipt, sent, err := sendOnce(ctx, rpcClient, wsClient, signer, cfg, selfPaid, opts)
	if err != nil {
		return nil, sent, errors.Join(paymasterErr, err)
	}

	return receipt, sent, nil
}

func shouldFallBack(ctx context.Context, paymaster Paymaster, err error) bool {
	return errors.Is(err, errPaymasterFailed) && paymaster.FallsBack() && ctx.Err() == nil
}

func signedBy(t *solana.Transaction, index int, key solana.PublicKey) bool {
	accounts, err := t.Message.Instructions[index].ResolveInstructionAccounts(&t.Message)
	if err != nil {
		return false
	}

	signs := func(account *solana.AccountMeta) bool {
		return account.IsSigner && account.PublicKey == key
	}

	return slices.ContainsFunc(accounts, signs)
}

func signWithPaymaster(
	ctx context.Context,
	t *solana.Transaction,
	lookup func(solana.PublicKey) *solana.PrivateKey,
	opts options,
) (*solana.Transaction, error) {
	for _, key := range t.Message.Signers()[1:] {
		if lookup(key) != nil {
			continue
		}

		if key == opts.rentPayer {
			return nil, fmt.Errorf("%w: signing: rent payer key %q not found", errPaymasterFailed, key)
		}

		return nil, fmt.Errorf("signing: signer key %q not found", key)
	}

	if _, err := t.PartialSign(lookup); err != nil {
		return nil, fmt.Errorf("signing: %w", err)
	}

	message, err := t.Message.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("serializing message: %w", err)
	}

	signed, err := opts.paymaster.Sign(ctx, t)
	if err != nil {
		return nil, fmt.Errorf("%w: signing: %w", errPaymasterFailed, err)
	}

	if signed == nil {
		return nil, fmt.Errorf("%w: %w", errPaymasterFailed, ErrPaymasterMessageChanged)
	}

	signedMessage, err := signed.Message.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("%w: %w: %w", errPaymasterFailed, ErrPaymasterSignatureMismatch, err)
	}

	if !bytes.Equal(signedMessage, message) {
		return nil, fmt.Errorf("%w: %w", errPaymasterFailed, ErrPaymasterMessageChanged)
	}

	if err := signed.VerifySignatures(); err != nil {
		return nil, fmt.Errorf("%w: %w: %w", errPaymasterFailed, ErrPaymasterSignatureMismatch, err)
	}

	return signed, nil
}

func toSelfPaid(
	ixs []solana.Instruction,
	keys []solana.PrivateKey,
	rentPayer solana.PublicKey,
) ([]solana.Instruction, error) {
	signer := keys[0].PublicKey()

	held := map[solana.PublicKey]bool{}
	for _, key := range keys {
		held[key.PublicKey()] = true
	}

	selfPaid := make([]solana.Instruction, len(ixs))
	for i, ix := range ixs {
		metas := ix.Accounts()
		accounts := make([]*solana.AccountMeta, len(metas))

		for j, meta := range metas {
			account := *meta
			if account.IsSigner && !held[account.PublicKey] {
				if account.PublicKey != rentPayer {
					return nil, fmt.Errorf("%w %s", errUnknownSigner, account.PublicKey)
				}

				account.PublicKey = signer
			}

			accounts[j] = &account
		}

		selfPaid[i] = selfPaidInstruction{
			accounts:    accounts,
			Instruction: ix,
		}
	}

	return selfPaid, nil
}

func withPaymaster(paymaster Paymaster, rentPayer solana.PublicKey) Option {
	return func(o *options) {
		o.paymaster = paymaster
		o.rentPayer = rentPayer
	}
}
