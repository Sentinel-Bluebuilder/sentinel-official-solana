package paymaster_test

import (
	"bytes"
	"net/http"
	"slices"
	"testing"

	bin "github.com/gagliardetto/binary"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/programs/system"
	"github.com/solana-foundation/solana-go/v2/programs/token"
	"github.com/solana-foundation/solana-go/v2/programs/token-2022"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/paymaster"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
)

const feeTokenDecimals = 6

type koraFixture struct {
	fakeKora    *fakeKora
	fakeRPC     *sdktest.FakeRPC
	kora        *paymaster.Kora
	mint        solana.PublicKey
	payerSigner paymaster.PayerSigner
}

func TestKora_FeeTransfer_FitsOverhead(t *testing.T) {
	t.Parallel()

	signer := solana.NewWallet().PublicKey()
	fixture := startKora(t, tx.PaymasterConfig{}, "margin", solana.Token2022ProgramID)
	ixs := []solana.Instruction{system.NewTransferInstruction(1, signer, signer).Build()}

	feeTransfer, err := fixture.kora.FeeTransfer(t.Context(), signer)
	require.NoError(t, err)

	withFeeTransfer := slices.Concat(ixs, feeTransfer.Instructions())
	paymasterPaid := transactionBytes(t, newTransaction(t, fixture.payerSigner.Signer, withFeeTransfer...))
	selfPaid := transactionBytes(t, newTransaction(t, signer, ixs...))
	require.LessOrEqual(t, paymasterPaid-selfPaid, paymaster.FeeTransferOverheadBytes)
}

func TestKora_FeeTransfer_Free(t *testing.T) {
	t.Parallel()

	fixture := startKora(t, tx.PaymasterConfig{}, "free", solana.Token2022ProgramID)

	feeTransfer, err := fixture.kora.FeeTransfer(t.Context(), solana.NewWallet().PublicKey())
	require.NoError(t, err)
	require.Nil(t, feeTransfer)
}

func TestKora_FeeTransfer_QuotesFeeToken(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		newTransfer  func(baseUnits uint64, source, mint, destination, owner solana.PublicKey) solana.Instruction
		tokenProgram solana.PublicKey
	}{
		{
			name: "token",
			newTransfer: func(baseUnits uint64, source, mint, destination, owner solana.PublicKey) solana.Instruction {
				return token.NewTransferCheckedInstruction(
					baseUnits,
					feeTokenDecimals,
					source,
					mint,
					destination,
					owner,
					nil,
				).Build()
			},
			tokenProgram: solana.TokenProgramID,
		},
		{
			name: "token-2022",
			newTransfer: func(baseUnits uint64, source, mint, destination, owner solana.PublicKey) solana.Instruction {
				return token2022.NewTransferCheckedInstruction(
					baseUnits,
					feeTokenDecimals,
					source,
					mint,
					destination,
					owner,
					nil,
				).Build()
			},
			tokenProgram: solana.Token2022ProgramID,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			signer := solana.NewWallet().PublicKey()
			fixture := startKora(t, tx.PaymasterConfig{}, "margin", tc.tokenProgram)
			paymentAddress := fixture.payerSigner.PaymentAddress

			source, _, err := solana.FindAssociatedTokenAddressWithProgram(signer, fixture.mint, tc.tokenProgram)
			require.NoError(t, err)
			destination, _, err := solana.FindAssociatedTokenAddressWithProgram(paymentAddress, fixture.mint, tc.tokenProgram)
			require.NoError(t, err)

			feeTransfer, err := fixture.kora.FeeTransfer(t.Context(), signer)
			require.NoError(t, err)

			placeholder := tc.newTransfer(0, source, fixture.mint, destination, signer)
			require.Equal(t, []solana.Instruction{placeholder}, feeTransfer.Instructions())

			tokenFee, quoted, err := feeTransfer.Quote(t.Context(), newTransaction(t, fixture.payerSigner.Signer))
			require.NoError(t, err)
			require.Equal(t, tx.TokenFee{BaseUnits: 1234, FeeToken: fixture.mint}, tokenFee)
			require.Equal(t, []solana.Instruction{tc.newTransfer(1234, source, fixture.mint, destination, signer)}, quoted)

			params := fixture.fakeKora.params["estimateTransactionFee"]
			require.Equal(t, fixture.mint.String(), params["fee_token"])
			require.Equal(t, fixture.payerSigner.Signer.String(), params["signer_key"])
		})
	}
}

func TestKora_FeeTransfer_RejectsAboveMax(t *testing.T) {
	t.Parallel()

	fixture := startKora(t, tx.PaymasterConfig{MaxFeeBaseUnits: 1000}, "margin", solana.Token2022ProgramID)

	feeTransfer, err := fixture.kora.FeeTransfer(t.Context(), solana.NewWallet().PublicKey())
	require.NoError(t, err)

	_, _, err = feeTransfer.Quote(t.Context(), newTransaction(t, fixture.payerSigner.Signer))
	require.ErrorIs(t, err, paymaster.ErrFeeExceedsMax)
}

func TestKora_FeeTransfer_RejectsInvalidFeeToken(t *testing.T) {
	t.Parallel()

	initializedMint := newAccountData(t, token.Mint{Decimals: feeTokenDecimals, IsInitialized: true})
	tokenAccount := newAccountData(t, token.Account{Amount: 1, Owner: solana.PublicKey{13: 1}, State: token.Initialized})

	cases := []struct {
		account *sdktest.Account
		name    string
	}{
		{
			name: "missing",
		},
		{
			account: &sdktest.Account{Data: initializedMint, Owner: solana.SystemProgramID},
			name:    "non-token owner",
		},
		{
			account: &sdktest.Account{Data: tokenAccount, Owner: solana.Token2022ProgramID},
			name:    "token account",
		},
		{
			account: &sdktest.Account{Data: make([]byte, token.MINT_SIZE), Owner: solana.TokenProgramID},
			name:    "uninitialized mint",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			feeToken := solana.NewWallet().PublicKey()
			cfg := tx.PaymasterConfig{FeeToken: feeToken.String()}
			fixture := startKora(t, cfg, "margin", solana.Token2022ProgramID)

			if tc.account != nil {
				fixture.fakeRPC.SetAccount(feeToken, *tc.account)
			}

			_, err := fixture.kora.FeeTransfer(t.Context(), solana.NewWallet().PublicKey())
			require.ErrorIs(t, err, validate.ErrInvalidConfig)
		})
	}
}

func TestKora_FeeTransfer_RejectsMismatchedEstimate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		field string
		name  string
	}{
		{field: "payment_address", name: "payment address"},
		{field: "signer_pubkey", name: "signer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fixture := startKora(t, tx.PaymasterConfig{}, "margin", solana.Token2022ProgramID)
			estimateResult := map[string]any{
				"fee_in_token":    1234,
				"payment_address": fixture.payerSigner.PaymentAddress,
				"signer_pubkey":   fixture.payerSigner.Signer,
			}
			estimateResult[tc.field] = solana.NewWallet().PublicKey()
			fixture.fakeKora.responses["estimateTransactionFee"] = newResponse(t, estimateResult)

			feeTransfer, err := fixture.kora.FeeTransfer(t.Context(), solana.NewWallet().PublicKey())
			require.NoError(t, err)

			_, _, err = feeTransfer.Quote(t.Context(), newTransaction(t, fixture.payerSigner.Signer))
			require.ErrorIs(t, err, paymaster.ErrEstimateMismatch)
		})
	}
}

func TestKora_Sign_ReportsDisabledMethod(t *testing.T) {
	t.Parallel()

	fixture := startKora(t, tx.PaymasterConfig{}, "margin", solana.Token2022ProgramID)
	fixture.fakeKora.status = http.StatusMethodNotAllowed

	_, err := fixture.kora.Sign(t.Context(), newTransaction(t, solana.NewWallet().PublicKey()))
	require.ErrorContains(t, err, "unexpected status 405: enable sign_transaction in kora's enabled_methods")
}

func TestNewKora_RejectsInvalidFeeToken(t *testing.T) {
	t.Parallel()

	_, err := paymaster.NewKora(nil, tx.PaymasterConfig{FeeToken: "invalid"}, solana.PublicKey{})
	require.ErrorIs(t, err, validate.ErrInvalidConfig)
}

func newAccountData(t *testing.T, account any) []byte {
	t.Helper()

	var data bytes.Buffer
	require.NoError(t, bin.NewBinEncoder(&data).Encode(account))

	return data.Bytes()
}

func startKora(t *testing.T, cfg tx.PaymasterConfig, priceType string, tokenProgram solana.PublicKey) koraFixture {
	t.Helper()

	mint := solana.NewWallet().PublicKey()
	payerSigner := paymaster.PayerSigner{
		PaymentAddress: solana.NewWallet().PublicKey(),
		Signer:         solana.NewWallet().PublicKey(),
	}
	estimateResult := map[string]any{
		"fee_in_lamports": 5000,
		"fee_in_token":    1234,
		"payment_address": payerSigner.PaymentAddress,
		"signer_pubkey":   payerSigner.Signer,
	}
	configResult := map[string]any{"validation_config": map[string]any{"price": map[string]any{"type": priceType}}}
	payerSignerResult := map[string]any{
		"payment_address": payerSigner.PaymentAddress,
		"signer_address":  payerSigner.Signer,
	}
	fakeKora, url := startFakeKora(t, map[string]string{
		"estimateTransactionFee": newResponse(t, estimateResult),
		"getConfig":              newResponse(t, configResult),
		"getPayerSigner":         newResponse(t, payerSignerResult),
	})

	mintData := newAccountData(t, token.Mint{Decimals: feeTokenDecimals, IsInitialized: true})

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.SetAccount(mint, sdktest.Account{Data: mintData, Owner: tokenProgram})

	cfg.URL = url

	kora, err := paymaster.NewKora(fakeRPC.NewClient(), cfg, mint)
	require.NoError(t, err)

	return koraFixture{fakeKora: fakeKora, fakeRPC: fakeRPC, kora: kora, mint: mint, payerSigner: payerSigner}
}

func transactionBytes(t *testing.T, transaction *solana.Transaction) int {
	t.Helper()

	raw, err := transaction.MarshalBinary()
	require.NoError(t, err)

	return len(raw)
}
