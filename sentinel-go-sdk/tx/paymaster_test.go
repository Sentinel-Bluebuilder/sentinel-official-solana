package tx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/programs/system"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

var errPaymasterUnreachable = errors.New("paymaster unreachable")

func TestSender_RentPayer(t *testing.T) {
	t.Parallel()

	signer := solana.PublicKey{1}
	paymasterKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	unreachable := sdktest.FakePaymaster{AddressErr: errPaymasterUnreachable}
	fallback := unreachable
	fallback.Fallback = true

	cases := []struct {
		name      string
		paymaster tx.Paymaster
		want      solana.PublicKey
		wantErr   error
	}{
		{name: "self paid", want: signer},
		{name: "paymaster", paymaster: sdktest.FakePaymaster{Key: paymasterKey}, want: paymasterKey.PublicKey()},
		{name: "unreachable with fallback", paymaster: fallback, want: signer},
		{name: "unreachable without fallback", paymaster: unreachable, wantErr: errPaymasterUnreachable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sender := &tx.Sender{Paymaster: tc.paymaster}
			rentPayer, err := sender.RentPayer(t.Context(), signer)
			require.ErrorIs(t, err, tc.wantErr)
			require.Equal(t, tc.want, rentPayer)
		})
	}
}

func TestSender_Send_PaymasterFailure(t *testing.T) {
	t.Parallel()

	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	paymasterKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	rotatedKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	placeholder := solana.NewInstruction(solana.SystemProgramID, nil, nil)
	feeTransfer := sdktest.FakeFeeTransfer{
		Placeholders: []solana.Instruction{placeholder},
		Quoted:       []solana.Instruction{placeholder},
	}
	failingFeeTransfer := feeTransfer
	failingFeeTransfer.QuoteErr = errPaymasterUnreachable
	mismatchedFeeTransfer := feeTransfer
	mismatchedFeeTransfer.Quoted = []solana.Instruction{placeholder, placeholder}

	failingSimulate := func(txErr any) func(*solana.Transaction) sdktest.SimulateResult {
		return func(probe *solana.Transaction) sdktest.SimulateResult {
			if probe.Message.AccountKeys[0] != signer.PublicKey() {
				return sdktest.SimulateResult{Err: txErr}
			}

			return sdktest.SimulateResult{UnitsConsumed: 5_000}
		}
	}
	feeTransferErr := json.RawMessage(`{"InstructionError":[1,"InvalidAccountData"]}`)
	rentErr := json.RawMessage(`{"InsufficientFundsForRent":{"account_index":0}}`)
	rentPayerErr := json.RawMessage(`{"InstructionError":[0,{"Custom":1}]}`)

	cases := []struct {
		canceled  bool
		name      string
		paymaster sdktest.FakePaymaster
		simulate  func(*solana.Transaction) sdktest.SimulateResult
		wantErr   error
	}{
		{
			name:      "address error",
			paymaster: sdktest.FakePaymaster{AddressErr: errPaymasterUnreachable},
			wantErr:   errPaymasterUnreachable,
		},
		{
			name:      "fee transfer error",
			paymaster: sdktest.FakePaymaster{FeeTransferErr: errPaymasterUnreachable},
			wantErr:   errPaymasterUnreachable,
		},
		{
			name:     "fee payer not found",
			simulate: failingSimulate("AccountNotFound"),
			wantErr:  tx.ErrPaymasterFailed,
		},
		{
			name:     "insufficient funds for fee",
			simulate: failingSimulate("InsufficientFundsForFee"),
			wantErr:  tx.ErrPaymasterFailed,
		},
		{
			name:     "invalid account for fee",
			simulate: failingSimulate("InvalidAccountForFee"),
			wantErr:  tx.ErrPaymasterFailed,
		},
		{
			name:     "insufficient funds for rent",
			simulate: failingSimulate(rentErr),
			wantErr:  tx.ErrPaymasterFailed,
		},
		{
			name:     "rent payer instruction error",
			simulate: failingSimulate(rentPayerErr),
			wantErr:  tx.ErrPaymasterFailed,
		},
		{
			name:      "fee transfer instruction error",
			paymaster: sdktest.FakePaymaster{Transfer: feeTransfer},
			simulate:  failingSimulate(feeTransferErr),
			wantErr:   tx.ErrPaymasterFailed,
		},
		{
			name:      "quote error",
			paymaster: sdktest.FakePaymaster{Transfer: failingFeeTransfer},
			wantErr:   errPaymasterUnreachable,
		},
		{
			name:      "quoted instruction count mismatch",
			paymaster: sdktest.FakePaymaster{Transfer: mismatchedFeeTransfer},
			wantErr:   tx.ErrFeeTransferInstructionsMismatch,
		},
		{
			name:      "sign error",
			paymaster: sdktest.FakePaymaster{SignErr: errPaymasterUnreachable},
			wantErr:   errPaymasterUnreachable,
		},
		{
			name:      "changed message",
			paymaster: sdktest.FakePaymaster{ChangesMessage: true},
			wantErr:   tx.ErrPaymasterMessageChanged,
		},
		{
			name:      "rotated signer",
			paymaster: sdktest.FakePaymaster{Key: rotatedKey},
			wantErr:   tx.ErrPaymasterFailed,
		},
		{
			canceled:  true,
			name:      "canceled context",
			paymaster: sdktest.FakePaymaster{AddressErr: errPaymasterUnreachable},
			wantErr:   errPaymasterUnreachable,
		},
	}
	for _, tc := range cases {
		for _, fallback := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s fallback %t", tc.name, fallback), func(t *testing.T) {
				t.Parallel()

				ctx, cancel := context.WithCancel(t.Context())
				t.Cleanup(cancel)

				var logs bytes.Buffer

				fakeRPC := sdktest.NewFakeRPC(t)
				fakeRPC.Simulate = tc.simulate
				sender := newSender(fakeRPC, sdktest.FakePaymaster{Key: paymasterKey})
				sender.Log = zerolog.New(&logs)

				rentPayer, err := sender.RentPayer(ctx, signer.PublicKey())
				require.NoError(t, err)

				paymaster := tc.paymaster
				paymaster.Fallback = fallback

				if paymaster.Key == nil {
					paymaster.Key = paymasterKey
				}

				if tc.canceled {
					paymaster.Cancel = cancel
				}

				sender.Paymaster = paymaster
				ixs := transferIxs(rentPayer, signer.PublicKey())

				receipt, err := sender.Send(ctx, signer, ixs, tx.WithLabel("transfer"))
				if !fallback || tc.canceled {
					require.ErrorIs(t, err, tc.wantErr)
					require.ErrorAs(t, err, new(*tx.UnsentError))
					require.Empty(t, fakeRPC.Sent())

					return
				}

				require.NoError(t, err)
				require.Equal(t, signer.PublicKey(), receipt.FeePayer)
				require.Contains(t, logs.String(), `"instruction":"transfer","error":"paymaster failed: `)
				require.Contains(t, logs.String(), `"message":"paying fees and rent with the signer"`)

				sent := fakeRPC.Sent()
				require.Len(t, sent, 1)
				require.NotContains(t, sent[0].Message.AccountKeys, paymasterKey.PublicKey())
			})
		}
	}
}

func TestSender_Send_PaymasterFeeTransfer(t *testing.T) {
	t.Parallel()

	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	paymasterKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	tokenFee := tx.TokenFee{BaseUnits: 1234, FeeToken: solana.PublicKey{1}}
	placeholder := system.NewTransferInstruction(0, signer.PublicKey(), paymasterKey.PublicKey()).Build()
	quoted := system.NewTransferInstruction(tokenFee.BaseUnits, signer.PublicKey(), paymasterKey.PublicKey()).Build()
	feeTransfer := sdktest.FakeFeeTransfer{
		Placeholders: []solana.Instruction{placeholder},
		Quoted:       []solana.Instruction{quoted},
		TokenFee:     tokenFee,
	}

	cases := []struct {
		count       int
		feeTransfer tx.FeeTransfer
		last        solana.Instruction
		name        string
		tokenFee    *tx.TokenFee
	}{
		{count: 2, feeTransfer: feeTransfer, last: quoted, name: "fee transfer", tokenFee: &tokenFee},
		{count: 1, last: selfTransferIxs(signer)[0], name: "free"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fakeRPC := sdktest.NewFakeRPC(t)
			sender := newSender(fakeRPC, sdktest.FakePaymaster{Key: paymasterKey, Transfer: tc.feeTransfer})

			receipt, err := sender.Send(t.Context(), signer, selfTransferIxs(signer))
			require.NoError(t, err)
			require.Equal(t, tc.tokenFee, receipt.TokenFee)

			sent := fakeRPC.Sent()
			require.Len(t, sent, 1)

			instructions := sent[0].Message.Instructions
			require.Len(t, instructions, tc.count)

			data, err := tc.last.Data()
			require.NoError(t, err)
			require.Equal(t, data, []byte(instructions[len(instructions)-1].Data))
		})
	}
}

func TestSender_Send_PaymasterKeepsProgramError(t *testing.T) {
	t.Parallel()

	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	paymasterKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	cases := []struct {
		addressErr          error
		name                string
		wantPaymasterFailed bool
	}{
		{name: "paymaster pays"},
		{
			addressErr:          errPaymasterUnreachable,
			name:                "signer pays after a paymaster failure",
			wantPaymasterFailed: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fakeRPC := sdktest.NewFakeRPC(t)
			fakeRPC.Simulate = func(*solana.Transaction) sdktest.SimulateResult {
				return sdktest.SimulateResult{
					Err: json.RawMessage(`{"InstructionError":[0,{"Custom":6000}]}`),
					Logs: []string{
						"Program " + dvpn.ProgramID.String() + " invoke [1]",
						"Program " + dvpn.ProgramID.String() + " failed: custom program error: 0x1770",
					},
				}
			}
			paymaster := sdktest.FakePaymaster{AddressErr: tc.addressErr, Fallback: true, Key: paymasterKey}
			ixs := []solana.Instruction{solana.NewInstruction(dvpn.ProgramID, nil, nil)}

			_, err := newSender(fakeRPC, paymaster).Send(t.Context(), signer, ixs)
			require.ErrorIs(t, err, dvpn.ErrUnauthorized)
			require.Equal(t, tc.wantPaymasterFailed, errors.Is(err, tx.ErrPaymasterFailed))
			require.ErrorAs(t, err, new(*tx.UnsentError))
			require.Empty(t, fakeRPC.Sent())
		})
	}
}

func TestSender_Send_PaymasterPaysFees(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	paymasterKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	sender := newSender(fakeRPC, sdktest.FakePaymaster{Key: paymasterKey})
	receipt, err := sender.Send(t.Context(), signer, selfTransferIxs(signer))
	require.NoError(t, err)
	require.Equal(t, paymasterKey.PublicKey(), receipt.FeePayer)

	sent := fakeRPC.Sent()
	require.Len(t, sent, 1)
	require.Equal(t, paymasterKey.PublicKey(), sent[0].Message.AccountKeys[0])
	require.NoError(t, sent[0].VerifySignatures())
}

func TestSender_Send_PaymasterRefusesUnknownSigner(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	fakeRPC := sdktest.NewFakeRPC(t)
	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	sender := newSender(fakeRPC, sdktest.FakePaymaster{AddressErr: errPaymasterUnreachable, Fallback: true})
	sender.Log = zerolog.New(&logs)
	forgotten := transferIxs(solana.PublicKey{1}, signer.PublicKey())

	_, err = sender.Send(t.Context(), signer, forgotten, tx.WithLabel("transfer"))
	require.ErrorIs(t, err, errPaymasterUnreachable)
	require.ErrorContains(t, err, "not falling back: unknown signer")
	require.ErrorAs(t, err, new(*tx.UnsentError))
	require.Empty(t, fakeRPC.Sent())
	require.Contains(t, logs.String(), `"level":"warn","instruction":"transfer","error":"unknown signer`)
}

func TestSender_Send_PaymasterRejectsMissingSignerKey(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	paymasterKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	missing := solana.PublicKey{1}
	sender := newSender(fakeRPC, sdktest.FakePaymaster{Key: paymasterKey})
	_, err = sender.Send(t.Context(), signer, transferIxs(missing, signer.PublicKey()))
	require.ErrorContains(t, err, fmt.Sprintf("signing: signer key %q not found", missing))
	require.ErrorAs(t, err, new(*tx.UnsentError))
	require.Empty(t, fakeRPC.Sent())
}

func TestSender_Send_PaymasterRetriesAfterExpiry(t *testing.T) {
	t.Parallel()

	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	paymasterKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	cases := []struct {
		failingSimulation int32
		name              string
		wantErr           error
		wantFeePayers     []solana.PublicKey
	}{
		{
			failingSimulation: 1,
			name:              "falls back once and asks the paymaster again",
			wantErr:           tx.ErrBlockhashExpired,
			wantFeePayers:     []solana.PublicKey{signer.PublicKey(), paymasterKey.PublicKey()},
		},
		{
			failingSimulation: 2,
			name:              "never falls back after broadcast",
			wantErr:           tx.ErrPaymasterFailed,
			wantFeePayers:     []solana.PublicKey{paymasterKey.PublicKey()},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var simulations atomic.Int32

			fakeRPC := sdktest.NewFakeRPC(t)
			fakeRPC.BlockHeight = 300
			fakeRPC.Unconfirmed = true
			fakeRPC.Simulate = func(probe *solana.Transaction) sdktest.SimulateResult {
				if probe.Message.AccountKeys[0] == paymasterKey.PublicKey() && simulations.Add(1) == tc.failingSimulation {
					return sdktest.SimulateResult{Err: "AccountNotFound"}
				}

				return sdktest.SimulateResult{UnitsConsumed: 5_000}
			}

			sender := newSender(fakeRPC, sdktest.FakePaymaster{Fallback: true, Key: paymasterKey})
			sender.Config.Retries = 1
			sender.Config.RetryDelay = 0

			_, err := sender.Send(t.Context(), signer, selfTransferIxs(signer))
			require.ErrorIs(t, err, tc.wantErr)
			require.NotErrorAs(t, err, new(*tx.UnsentError))

			sent := fakeRPC.Sent()
			feePayers := make([]solana.PublicKey, 0, len(sent))

			for _, transaction := range sent {
				feePayers = append(feePayers, transaction.Message.AccountKeys[0])
			}

			require.Equal(t, tc.wantFeePayers, feePayers)
		})
	}
}

func newSender(fakeRPC *sdktest.FakeRPC, paymaster tx.Paymaster) *tx.Sender {
	return &tx.Sender{
		Config:    tx.DefaultConfig(),
		Paymaster: paymaster,
		RPC:       fakeRPC.NewClient(),
	}
}
