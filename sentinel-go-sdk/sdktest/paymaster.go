package sdktest

import (
	"context"
	"fmt"

	"github.com/solana-foundation/solana-go/v2"

	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
)

var _ tx.FeeTransfer = (*FakeFeeTransfer)(nil)

type FakeFeeTransfer struct {
	Placeholders []solana.Instruction
	Quoted       []solana.Instruction
	QuoteErr     error
	TokenFee     tx.TokenFee
}

func (f FakeFeeTransfer) Instructions() []solana.Instruction {
	return f.Placeholders
}

func (f FakeFeeTransfer) Quote(context.Context, *solana.Transaction) (tx.TokenFee, []solana.Instruction, error) {
	return f.TokenFee, f.Quoted, f.QuoteErr
}

var _ tx.Paymaster = (*FakePaymaster)(nil)

type FakePaymaster struct {
	AddressErr     error
	Cancel         context.CancelFunc
	ChangesMessage bool
	Fallback       bool
	FeeTransferErr error
	Key            solana.PrivateKey
	SignErr        error
	Transfer       tx.FeeTransfer
}

func (f FakePaymaster) Address(context.Context) (solana.PublicKey, error) {
	if f.Cancel != nil {
		f.Cancel()
	}

	if f.AddressErr != nil {
		return solana.PublicKey{}, f.AddressErr
	}

	return f.Key.PublicKey(), nil
}

func (f FakePaymaster) FallsBack() bool {
	return f.Fallback
}

func (f FakePaymaster) FeeTransfer(context.Context, solana.PublicKey) (tx.FeeTransfer, error) {
	return f.Transfer, f.FeeTransferErr
}

func (f FakePaymaster) Sign(_ context.Context, t *solana.Transaction) (*solana.Transaction, error) {
	if f.SignErr != nil {
		return nil, f.SignErr
	}

	if f.ChangesMessage {
		t.Message.RecentBlockhash = solana.Hash{1}
	}

	lookup := func(key solana.PublicKey) *solana.PrivateKey {
		if key == f.Key.PublicKey() {
			return &f.Key
		}

		return nil
	}
	if _, err := t.PartialSign(lookup); err != nil {
		return nil, fmt.Errorf("signing: %w", err)
	}

	return t, nil
}
