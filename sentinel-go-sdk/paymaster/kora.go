package paymaster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	bin "github.com/gagliardetto/binary"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/programs/token"
	"github.com/solana-foundation/solana-go/v2/programs/token-2022"
	"github.com/solana-foundation/solana-go/v2/rpc"

	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
)

const (
	FeeTransferComputeUnits  uint32 = 10_000
	FeeTransferOverheadBytes int    = 256
)

const payerSignerTTL = 30 * time.Second

var (
	errEstimateMismatch = errors.New("estimate mismatch")
	errFeeExceedsMax    = errors.New("fee exceeds max")
)

var _ tx.Paymaster = (*Kora)(nil)

type Kora struct {
	client               *Client
	fallback             bool
	feeToken             solana.PublicKey
	maxFeeBaseUnits      uint64
	mu                   sync.Mutex
	payerSigner          PayerSigner
	payerSignerExpiresAt time.Time
	rpcClient            *rpc.Client
}

func NewKora(rpcClient *rpc.Client, cfg tx.PaymasterConfig, mint solana.PublicKey) (*Kora, error) {
	feeToken := mint

	if cfg.FeeToken != "" {
		parsed, err := solana.PublicKeyFromBase58(cfg.FeeToken)
		if err != nil {
			return nil, fmt.Errorf("%w: fee_token: %w", validate.ErrInvalidConfig, err)
		}

		feeToken = parsed
	}

	kora := &Kora{
		client:          NewClient(cfg.URL, cfg.APIKey),
		fallback:        cfg.Fallback,
		feeToken:        feeToken,
		maxFeeBaseUnits: cfg.MaxFeeBaseUnits,
		rpcClient:       rpcClient,
	}

	return kora, nil
}

func (k *Kora) Address(ctx context.Context) (solana.PublicKey, error) {
	payerSigner, err := k.loadPayerSigner(ctx)
	if err != nil {
		return solana.PublicKey{}, err
	}

	return payerSigner.Signer, nil
}

func (k *Kora) FallsBack() bool {
	return k.fallback
}

func (k *Kora) FeeTransfer(ctx context.Context, signer solana.PublicKey) (tx.FeeTransfer, error) {
	price, err := k.client.Price(ctx)
	if err != nil {
		return nil, err
	}

	if price.Free() {
		return nil, nil
	}

	payerSigner, err := k.loadPayerSigner(ctx)
	if err != nil {
		return nil, err
	}

	tokenProgram, decimals, err := fetchMint(ctx, k.rpcClient, k.feeToken)
	if err != nil {
		return nil, err
	}

	source, _, err := solana.FindAssociatedTokenAddressWithProgram(signer, k.feeToken, tokenProgram)
	if err != nil {
		return nil, fmt.Errorf("deriving fee token account: %w", err)
	}

	paymentAddress := payerSigner.PaymentAddress

	destination, _, err := solana.FindAssociatedTokenAddressWithProgram(paymentAddress, k.feeToken, tokenProgram)
	if err != nil {
		return nil, fmt.Errorf("deriving payment token account: %w", err)
	}

	transfer := feeTransfer{
		decimals:       decimals,
		destination:    destination,
		kora:           k,
		owner:          signer,
		paymentAddress: paymentAddress,
		source:         source,
		tokenProgram:   tokenProgram,
	}

	return transfer, nil
}

func (k *Kora) Sign(ctx context.Context, t *solana.Transaction) (*solana.Transaction, error) {
	signed, err := k.client.SignTransaction(ctx, t, t.Message.AccountKeys[0])
	if statusErr, ok := errors.AsType[*statusError](err); ok && statusErr.status == http.StatusMethodNotAllowed {
		return nil, fmt.Errorf("%w: enable sign_transaction in kora's enabled_methods", err)
	}

	return signed, err
}

func (k *Kora) loadPayerSigner(ctx context.Context) (PayerSigner, error) {
	now := time.Now()

	k.mu.Lock()
	payerSigner, expiresAt := k.payerSigner, k.payerSignerExpiresAt
	k.mu.Unlock()

	if now.Before(expiresAt) {
		return payerSigner, nil
	}

	payerSigner, err := k.client.PayerSigner(ctx)
	if err != nil {
		return PayerSigner{}, err
	}

	k.mu.Lock()
	k.payerSigner, k.payerSignerExpiresAt = payerSigner, now.Add(payerSignerTTL)
	k.mu.Unlock()

	return payerSigner, nil
}

type feeTransfer struct {
	decimals       uint8
	destination    solana.PublicKey
	kora           *Kora
	owner          solana.PublicKey
	paymentAddress solana.PublicKey
	source         solana.PublicKey
	tokenProgram   solana.PublicKey
}

func (f feeTransfer) Instructions() []solana.Instruction {
	return []solana.Instruction{f.transfer(0)}
}

func (f feeTransfer) Quote(ctx context.Context, t *solana.Transaction) (tx.TokenFee, []solana.Instruction, error) {
	feePayer := t.Message.AccountKeys[0]

	estimate, err := f.kora.client.EstimateTransactionFee(ctx, t, f.kora.feeToken, feePayer)
	if err != nil {
		return tx.TokenFee{}, nil, err
	}

	if estimate.Signer != feePayer || estimate.PaymentAddress != f.paymentAddress {
		return tx.TokenFee{}, nil, fmt.Errorf(
			"%w: signer %s, payment address %s",
			errEstimateMismatch,
			estimate.Signer,
			estimate.PaymentAddress,
		)
	}

	if estimate.FeeBaseUnits == nil {
		return tx.TokenFee{}, nil, errors.New("paymaster estimateTransactionFee: missing fee_in_token")
	}

	baseUnits, maxFeeBaseUnits := *estimate.FeeBaseUnits, f.kora.maxFeeBaseUnits
	if maxFeeBaseUnits > 0 && baseUnits > maxFeeBaseUnits {
		return tx.TokenFee{}, nil, fmt.Errorf("%w: %d > %d base units", errFeeExceedsMax, baseUnits, maxFeeBaseUnits)
	}

	tokenFee := tx.TokenFee{
		BaseUnits: baseUnits,
		FeeToken:  f.kora.feeToken,
	}

	return tokenFee, []solana.Instruction{f.transfer(baseUnits)}, nil
}

func (f feeTransfer) transfer(baseUnits uint64) solana.Instruction {
	mint := f.kora.feeToken
	if f.tokenProgram == solana.TokenProgramID {
		return token.NewTransferCheckedInstruction(
			baseUnits,
			f.decimals,
			f.source,
			mint,
			f.destination,
			f.owner,
			nil,
		).Build()
	}

	return token2022.NewTransferCheckedInstruction(
		baseUnits,
		f.decimals,
		f.source,
		mint,
		f.destination,
		f.owner,
		nil,
	).Build()
}

func fetchMint(ctx context.Context, rpcClient *rpc.Client, mint solana.PublicKey) (solana.PublicKey, uint8, error) {
	opts := rpc.GetAccountInfoOpts{Commitment: rpc.CommitmentConfirmed}

	account, err := rpcClient.GetAccountInfoWithOpts(ctx, mint, &opts)
	if errors.Is(err, rpc.ErrNotFound) {
		return solana.PublicKey{}, 0, fmt.Errorf("%w: fee_token %s not found", validate.ErrInvalidConfig, mint)
	}

	if err != nil {
		return solana.PublicKey{}, 0, fmt.Errorf("fetching fee token mint %s: %w", mint, err)
	}

	owner := account.Value.Owner

	decimals, ok := parseMintDecimals(owner, account.Value.Data.GetBinary())
	if !ok {
		return solana.PublicKey{}, 0, fmt.Errorf("%w: fee_token %s is not a mint", validate.ErrInvalidConfig, mint)
	}

	return owner, decimals, nil
}

func parseMintDecimals(owner solana.PublicKey, data []byte) (uint8, bool) {
	tokenOwned := owner == solana.TokenProgramID || owner == solana.Token2022ProgramID
	mintSized := len(data) == token.MINT_SIZE ||
		(owner == solana.Token2022ProgramID && len(data) > token2022.ACCOUNT_SIZE &&
			data[token2022.ACCOUNT_SIZE] == token2022.AccountTypeMint)

	if !tokenOwned || !mintSized {
		return 0, false
	}

	var mint token.Mint
	if err := bin.NewBinDecoder(data[:token.MINT_SIZE]).Decode(&mint); err != nil || !mint.IsInitialized {
		return 0, false
	}

	return mint.Decimals, true
}
