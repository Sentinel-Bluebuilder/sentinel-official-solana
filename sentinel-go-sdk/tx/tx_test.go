package tx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/programs/system"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/solana-foundation/solana-go/v2/rpc/jsonrpc"
	"github.com/solana-foundation/solana-go/v2/rpc/ws"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/sentinel-official/sentinel-programs/clients/go/distributor"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestClassify(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		err       error
		transient bool
	}{
		{err: &net.OpError{Err: errors.New("connection reset"), Net: "tcp", Op: "read"}, name: "network", transient: true},
		{err: io.EOF, name: "eof", transient: true},
		{err: io.ErrUnexpectedEOF, name: "unexpected_eof", transient: true},
		{err: errors.New("test error"), name: "other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.transient {
				require.ErrorIs(t, tx.Classify(fmt.Errorf("sending: %w", tc.err)), tx.ErrRPCTransient, tc.err.Error())

				return
			}

			require.NotErrorIs(t, tx.Classify(tc.err), tx.ErrRPCTransient)
		})
	}
}

func TestClassify_FormatsRPCErrorAndPreservesCause(t *testing.T) {
	t.Parallel()

	rpcErr := &jsonrpc.RPCError{Code: -32603, Message: "internal error"}
	marker := errors.New("request marker")
	err := tx.Classify(fmt.Errorf("request context: %w", errors.Join(marker, rpcErr)))
	require.ErrorContains(t, err, "request context")
	require.ErrorContains(t, err, "rpc error -32603: internal error")
	require.NotContains(t, err.Error(), "(*jsonrpc.RPCError)")
	require.ErrorIs(t, err, marker)

	var gotRPC *jsonrpc.RPCError
	require.ErrorAs(t, err, &gotRPC)
	require.Same(t, rpcErr, gotRPC)
}

func TestConfig_Validate(t *testing.T) {
	t.Parallel()

	invalidMode := tx.DefaultConfig()
	invalidMode.PriorityFeeMode = "invalid"
	invalidTimeout := tx.DefaultConfig()
	invalidTimeout.ConfirmTimeout = 0
	invalidMaxBytes := tx.DefaultConfig()
	invalidMaxBytes.MaxTransactionBytes = 0

	cases := []struct {
		name    string
		config  tx.Config
		message string
		invalid bool
	}{
		{config: tx.DefaultConfig(), name: "valid"},
		{config: invalidMode, invalid: true, message: "priority_fee_mode oneof=auto fixed never", name: "priority_fee_mode"},
		{config: invalidTimeout, message: "confirm_timeout gt=0", name: "confirm_timeout"},
		{config: invalidMaxBytes, invalid: true, message: "max_transaction_bytes gt=0", name: "max_transaction_bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.config.Validate()
			if tc.message == "" {
				require.NoError(t, err)

				return
			}

			if tc.invalid {
				require.ErrorIs(t, err, validate.ErrInvalidConfig)
			}

			require.ErrorContains(t, err, tc.message)
		})
	}
}

func TestEstimatePriorityFee(t *testing.T) {
	t.Parallel()

	samples := make([]rpc.PriorizationFeeResult, 0, 30)

	for i := range 30 {
		sample := rpc.PriorizationFeeResult{
			PrioritizationFee: uint64(1_000 * (i + 1)),
			Slot:              uint64(i),
		}
		samples = append(samples, sample)
	}

	low := []rpc.PriorizationFeeResult{
		{
			PrioritizationFee: 10,
			Slot:              1,
		},
		{
			PrioritizationFee: 20,
			Slot:              2,
		},
	}

	cases := []struct {
		name        string
		samples     []rpc.PriorizationFeeResult
		capLamports uint64
		want        uint64
	}{
		{capLamports: 1_000_000, name: "empty"},
		{capLamports: 1_000_000, name: "percentile", samples: samples, want: 20_000 * 200_000 / 1_000_000},
		{capLamports: 100, name: "capped", samples: samples, want: 100},
		{capLamports: 1_000_000, name: "below_threshold", samples: low},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, tx.EstimatePriorityFee(tc.samples, 200_000, tc.capLamports))
		})
	}
}

func TestSend_ConfirmsByPolling(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.PrioritizationFees = []uint64{5_000, 6_000, 7_000}

	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	cfg := tx.DefaultConfig()

	receipt, err := tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, cfg, selfTransferIxs(signer))
	require.NoError(t, err)
	require.NotEqual(t, solana.Signature{}, receipt.Signature)
	require.EqualValues(t, 100, receipt.Slot)

	sent := fakeRPC.Sent()
	require.Len(t, sent, 1)
	require.Equal(t, receipt.Signature, sent[0].Signatures[0])
	require.Equal(t, solana.MessageVersionV1, sent[0].Message.GetVersion())
}

func TestSend_DecodesSimulatedSystemError(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.Simulate = func(*solana.Transaction) sdktest.SimulateResult {
		return sdktest.SimulateResult{
			Err: map[string]any{"InstructionError": []any{0, map[string]any{"Custom": 0}}},
			Logs: []string{
				"Program " + solana.SystemProgramID.String() + " invoke [1]",
				"Program " + solana.SystemProgramID.String() + " failed: custom program error: 0x0",
			},
		}
	}
	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	ixs := []solana.Instruction{
		system.NewCreateAccountInstruction(
			1, 0, solana.SystemProgramID, signer.PublicKey(), signer.PublicKey(),
		).Build(),
	}
	_, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, tx.DefaultConfig(), ixs)
	require.ErrorContains(t, err, "system error: AccountAlreadyInUse (0)")
	require.NotContains(t, err.Error(), "sending transaction")
	require.NotContains(t, err.Error(), "map[")
	require.Empty(t, fakeRPC.Sent())
}

func TestSend_DoesNotAttributeSimulatedOtherProgramErrorToDVPN(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.Simulate = func(*solana.Transaction) sdktest.SimulateResult {
		return sdktest.SimulateResult{
			Err: map[string]any{"InstructionError": []any{0, map[string]any{"Custom": 6000}}},
			Logs: []string{
				"Program " + distributor.ProgramID.String() + " invoke [1]",
				"Program log: AnchorError occurred. Error Code: Unauthorized. Error Number: 6000. " +
					"Error Message: signer is not the program upgrade authority.",
				"Program " + distributor.ProgramID.String() + " failed: custom program error: 0x1770",
			},
		}
	}
	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	programData, err := core.FindProgramDataAddress(distributor.ProgramID)
	require.NoError(t, err)
	ix, err := distributor.BuildSetTotalClaimableInstruction(distributor.ProgramID, distributor.SetTotalClaimableParams{
		UpgradeAuthority: signer.PublicKey(),
		ProgramData:      programData,
	})
	require.NoError(t, err)
	_, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, tx.DefaultConfig(), []solana.Instruction{ix})
	require.NotErrorIs(t, err, dvpn.ErrUnauthorized)

	var txErr *core.TxError
	require.ErrorAs(t, err, &txErr)
	require.Equal(t, distributor.ProgramID.String(), txErr.Program)
}

func TestSend_DoesNotAttributeUnrelatedProgramErrorToDVPN(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.LandedErr = map[string]any{"InstructionError": []any{0, map[string]any{"Custom": 6000}}}
	fakeRPC.Logs = []string{
		"Program " + solana.SystemProgramID.String() + " invoke [1]",
		"Program " + solana.Token2022ProgramID.String() + " invoke [2]",
		"Program log: AnchorError occurred. Error Code: Unauthorized. Error Number: 6000. Error Message: access denied.",
		"Program " + solana.Token2022ProgramID.String() + " failed: custom program error: 0x1770",
		"Program " + solana.SystemProgramID.String() + " failed: custom program error: 0x1770",
	}
	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	_, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, tx.DefaultConfig(), selfTransferIxs(signer))

	var txErr *core.TxError
	require.ErrorAs(t, err, &txErr)
	require.Equal(t, "token-2022", txErr.Program)
	require.NotErrorIs(t, err, dvpn.ErrUnauthorized)
}

func TestSend_FailedLandingRetainsEarlierIndeterminateAttempt(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.BlockHeight = 300
	fakeRPC.Unconfirmed = true
	simulations := 0
	fakeRPC.Simulate = func(*solana.Transaction) sdktest.SimulateResult {
		simulations++
		if simulations == 2 {
			fakeRPC.Unconfirmed = false
			fakeRPC.LandedErr = map[string]any{"InstructionError": []any{0, map[string]any{"Custom": 6000}}}
		}

		return sdktest.SimulateResult{UnitsConsumed: 5_000}
	}
	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	cfg := tx.DefaultConfig()
	cfg.Retries = 1
	cfg.RetryDelay = 0
	ixs := []solana.Instruction{solana.NewInstruction(dvpn.ProgramID, nil, nil)}
	_, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, cfg, ixs)

	var failed *tx.FailedOnChainError
	require.ErrorAs(t, err, &failed)
	require.True(t, failed.PriorIndeterminate)
}

func TestSend_LandedCustomWithoutLogsUsesGeneratedErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		program solana.PublicKey
		want    error
	}{
		{name: "dvpn", program: dvpn.ProgramID, want: dvpn.ErrUnauthorized},
		{name: "distributor", program: distributor.ProgramID, want: distributor.ErrUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, fetchFails := range []bool{false, true} {
				fakeRPC := sdktest.NewFakeRPC(t)
				fakeRPC.LandedErr = map[string]any{"InstructionError": []any{0, map[string]any{"Custom": 6000}}}
				fakeRPC.TransactionRPCError = fetchFails
				signer, err := solana.NewRandomPrivateKey()
				require.NoError(t, err)

				ixs := []solana.Instruction{solana.NewInstruction(tc.program, nil, nil)}
				_, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, tx.DefaultConfig(), ixs)
				require.ErrorIs(t, err, tc.want)

				if fetchFails {
					require.ErrorContains(t, err, "transaction unavailable")
				}
			}
		})
	}
}

func TestSend_PreparationFailureIsUnsent(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.Simulate = func(*solana.Transaction) sdktest.SimulateResult {
		return sdktest.SimulateResult{Err: "simulation rejected"}
	}
	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	_, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, tx.DefaultConfig(), selfTransferIxs(signer))

	var unsent *tx.UnsentError
	require.ErrorAs(t, err, &unsent)
	require.Empty(t, fakeRPC.Sent())
}

func TestSend_ProcessedFailureWaitsForConfirmed(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		advanceTo  string
		commitment rpc.CommitmentType
		name       string
		wantFailed bool
	}{
		{commitment: rpc.CommitmentConfirmed, name: "processed remains indeterminate"},
		{
			advanceTo: "confirmed", commitment: rpc.CommitmentConfirmed,
			name: "confirmed failure is definitive", wantFailed: true,
		},
		{advanceTo: "confirmed", commitment: rpc.CommitmentFinalized, name: "finalized waits for finality"},
		{
			advanceTo: "finalized", commitment: rpc.CommitmentFinalized,
			name: "finalized failure is definitive", wantFailed: true,
		},
		{commitment: rpc.CommitmentProcessed, name: "processed setting still needs confirmation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fakeRPC := sdktest.NewFakeRPC(t)

			var statusCalls atomic.Int32

			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)

					return
				}

				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				if err := json.Unmarshal(body, &request); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)

					return
				}

				if request.Method == "getSignatureStatuses" {
					call := statusCalls.Add(1)

					status := "processed"
					if tc.advanceTo != "" && call > 1 {
						status = tc.advanceTo
					}

					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(w,
						`{"jsonrpc":"2.0","id":%s,"result":{"context":{"slot":100},"value":[`+
							`{"slot":100,"confirmationStatus":%q,"err":{"InstructionError":[0,{"Custom":6000}]}}]}}`,
						request.ID, status,
					)

					return
				}

				r.Body = io.NopCloser(bytes.NewReader(body))
				fakeRPC.ServeHTTP(w, r)
			}))
			t.Cleanup(proxy.Close)

			signer, err := solana.NewRandomPrivateKey()
			require.NoError(t, err)

			cfg := tx.DefaultConfig()
			cfg.Commitment = tc.commitment
			cfg.ConfirmTimeout = 3 * time.Second
			_, err = tx.Send(
				t.Context(), rpc.New(proxy.URL), nil, signer, cfg,
				[]solana.Instruction{solana.NewInstruction(dvpn.ProgramID, nil, nil)},
			)

			if tc.wantFailed {
				var failed *tx.FailedOnChainError
				require.ErrorAs(t, err, &failed)
				require.GreaterOrEqual(t, statusCalls.Load(), int32(2))

				return
			}

			require.ErrorIs(t, err, tx.ErrOutcomeIndeterminate)
			require.GreaterOrEqual(t, statusCalls.Load(), int32(1))
		})
	}
}

func TestSend_RebuildsAfterExpiry(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.BlockHeight = 300
	fakeRPC.Unconfirmed = true

	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	cfg := tx.DefaultConfig()
	cfg.RetryDelay = 0
	cfg.Retries = 1

	_, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, cfg, selfTransferIxs(signer))
	require.ErrorIs(t, err, tx.ErrBlockhashExpired)

	sent := fakeRPC.Sent()
	require.Len(t, sent, 2)

	distinct := map[solana.Signature]struct{}{}
	for _, sentTx := range sent {
		distinct[sentTx.Signatures[0]] = struct{}{}
	}

	require.Len(t, distinct, 1)
}

func TestSend_RejectsEmpty(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)

	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	_, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, tx.DefaultConfig(), nil)
	require.ErrorIs(t, err, tx.ErrMissingInstructions)
	require.ErrorAs(t, err, new(*tx.UnsentError))
}

func TestSend_RejectsOversizedTransaction(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	cfg := tx.DefaultConfig()
	cfg.MaxTransactionBytes = 300

	ixs := make([]solana.Instruction, 40)
	for i := range ixs {
		ixs[i] = selfTransferIxs(signer)[0]
	}

	_, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, cfg, ixs)
	require.ErrorIs(t, err, tx.ErrTransactionTooLarge)
	require.Empty(t, fakeRPC.Sent())

	cfg.MaxTransactionBytes = 5000
	largeIx := solana.NewInstruction(solana.SystemProgramID, nil, make([]byte, 4200))
	_, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, cfg, []solana.Instruction{largeIx})
	require.ErrorIs(t, err, tx.ErrTransactionTooLarge)
	require.Empty(t, fakeRPC.Sent())
}

func TestSend_RetryPreparationFailureKeepsEarlierSendIndeterminate(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.BlockHeight = 300
	fakeRPC.Unconfirmed = true
	simulations := 0
	fakeRPC.Simulate = func(*solana.Transaction) sdktest.SimulateResult {
		simulations++
		if simulations == 2 {
			return sdktest.SimulateResult{Err: "simulation rejected"}
		}

		return sdktest.SimulateResult{UnitsConsumed: 5_000}
	}
	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	cfg := tx.DefaultConfig()
	cfg.Retries = 1
	cfg.RetryDelay = 0

	_, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, cfg, selfTransferIxs(signer))

	var unsent *tx.UnsentError
	require.NotErrorAs(t, err, &unsent)
	require.Len(t, fakeRPC.Sent(), 1)
}

func TestSend_ReturnsFinalLandedDVPNError(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.LandedErr = map[string]any{"InstructionError": []any{0, map[string]any{"Custom": 6001}}}
	fakeRPC.Logs = []string{
		"Program " + dvpn.ProgramID.String() + " invoke [1]",
		"Program " + solana.Token2022ProgramID.String() + " invoke [2]",
		"Program log: AnchorError occurred. Error Code: Unauthorized. Error Number: 6000. Error Message: unauthorized.",
		"Program " + solana.Token2022ProgramID.String() + " failed: custom program error: 0x1770",
		"Program log: handled child failure",
		"Program log: AnchorError occurred. Error Code: OperationPaused. Error Number: 6001. Error Message: paused.",
		"Program " + dvpn.ProgramID.String() + " failed: custom program error: 0x1771",
	}
	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	ixs := []solana.Instruction{solana.NewInstruction(dvpn.ProgramID, nil, nil)}
	_, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, tx.DefaultConfig(), ixs)
	require.ErrorIs(t, err, dvpn.ErrOperationPaused)
	require.NotErrorIs(t, err, dvpn.ErrUnauthorized)
}

func TestSend_ReturnsLandedErrorWhenTransactionFetchFails(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.LandedErr = map[string]any{"InstructionError": []any{0, map[string]any{"Custom": 2006}}}
	fakeRPC.TransactionRPCError = true
	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	_, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, tx.DefaultConfig(), selfTransferIxs(signer))

	var txErr *core.TxError
	require.ErrorAs(t, err, &txErr)
	require.Contains(t, txErr.Raw, "InstructionError")
	require.ErrorContains(t, err, "transaction unavailable")
}

func TestSend_ReturnsLandedProgramError(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.LandedErr = map[string]any{"InstructionError": []any{0, map[string]any{"Custom": 2006}}}
	fakeRPC.Logs = []string{
		"Program log: AnchorError caused by account: node. Error Code: ConstraintSeeds. " +
			"Error Number: 2006. Error Message: A seeds constraint was violated.",
	}
	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	_, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, tx.DefaultConfig(), selfTransferIxs(signer))

	var txErr *core.TxError
	require.ErrorAs(t, err, &txErr)
	require.Equal(t, "ConstraintSeeds", txErr.Name)
	require.EqualValues(t, 2006, txErr.Code)
	require.NotContains(t, err.Error(), "failed on chain")
}

func TestSend_ReturnsProgramError(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.Simulate = func(*solana.Transaction) sdktest.SimulateResult {
		result := sdktest.SimulateResult{
			Err: map[string]any{"InstructionError": []any{0, map[string]any{"Custom": 6000}}},
			Logs: []string{
				"Program " + dvpn.ProgramID.String() + " invoke [1]",
				"Program log: AnchorError occurred. Error Code: Unauthorized. Error Number: 6000. Error Message: unauthorized.",
				"Program " + dvpn.ProgramID.String() + " failed: custom program error: 0x1770",
			},
		}

		return result
	}

	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	ixs := []solana.Instruction{solana.NewInstruction(dvpn.ProgramID, nil, nil)}
	_, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, tx.DefaultConfig(), ixs)
	require.ErrorIs(t, err, dvpn.ErrUnauthorized)
	require.Empty(t, fakeRPC.Sent())
}

func TestSend_ReturnsStatusErrorWhenTransactionMetadataIsIncomplete(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		metaErrMissing bool
		metaMissing    bool
	}{
		{metaErrMissing: true, name: "nil_error"},
		{metaMissing: true, name: "nil_metadata"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fakeRPC := sdktest.NewFakeRPC(t)
			fakeRPC.LandedErr = map[string]any{"InstructionError": []any{0, map[string]any{"Custom": 2006}}}
			fakeRPC.TransactionMetaErrMissing = tc.metaErrMissing
			fakeRPC.TransactionMetaMissing = tc.metaMissing
			signer, err := solana.NewRandomPrivateKey()
			require.NoError(t, err)

			_, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, tx.DefaultConfig(), selfTransferIxs(signer))

			var txErr *core.TxError
			require.ErrorAs(t, err, &txErr)
			require.NotErrorIs(t, err, rpc.ErrNotFound)
		})
	}
}

func TestSend_TransactionByteBoundary(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	ixs := selfTransferIxs(signer)
	cfg := tx.DefaultConfig()
	cfg.PriorityFeeMode = tx.PriorityFeeModeNever

	_, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, cfg, ixs)
	require.NoError(t, err)

	sent := fakeRPC.Sent()
	require.Len(t, sent, 1)
	raw, err := sent[0].MarshalBinary()
	require.NoError(t, err)

	cfg.MaxTransactionBytes = len(raw)
	_, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, cfg, ixs)
	require.NoError(t, err)
	require.Len(t, fakeRPC.Sent(), 2)

	cfg.MaxTransactionBytes--
	_, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, cfg, ixs)
	require.ErrorIs(t, err, tx.ErrTransactionTooLarge)
	require.Len(t, fakeRPC.Sent(), 2)
}

func TestSend_UsesLandedLogs(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.Simulate = func(*solana.Transaction) sdktest.SimulateResult {
		return sdktest.SimulateResult{
			Logs:          []string{"simulated"},
			UnitsConsumed: 5_000,
		}
	}
	fakeRPC.Logs = []string{"landed"}
	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	receipt, err := tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, tx.DefaultConfig(), selfTransferIxs(signer))
	require.NoError(t, err)
	require.Equal(t, []string{"landed"}, receipt.Logs)
	require.False(t, receipt.LogsSimulated)

	cfg := tx.DefaultConfig()
	cfg.Commitment = rpc.CommitmentProcessed
	receipt, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, cfg, selfTransferIxs(signer))
	require.NoError(t, err)
	require.Equal(t, []string{"landed"}, receipt.Logs)
	require.False(t, receipt.LogsSimulated)

	fakeRPC.TransactionMissing = true
	receipt, err = tx.Send(t.Context(), fakeRPC.NewClient(), nil, signer, tx.DefaultConfig(), selfTransferIxs(signer))
	require.NoError(t, err)
	require.Equal(t, []string{"simulated"}, receipt.Logs)
	require.True(t, receipt.LogsSimulated)
}

func TestSender_Send_ConfirmsByPollingWhenWSUnreachable(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	fakeRPC := sdktest.NewFakeRPC(t)
	cfg := cli.RPCConfig{Timeout: time.Second, WSURL: "ws://127.0.0.1:1"}
	sender := tx.NewSender(fakeRPC.NewClient(), cfg.DialWS, tx.DefaultConfig(), zerolog.New(&buf))
	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	pub := signer.PublicKey()
	ixs := []solana.Instruction{system.NewTransferInstruction(1, pub, pub).Build()}

	_, err = sender.Send(t.Context(), signer, ixs)
	require.NoError(t, err)
	require.Len(t, fakeRPC.Sent(), 1)
	require.Contains(t, buf.String(), "connecting websocket ws://127.0.0.1:1")
}

func TestSender_Send_DecodesConfiguredProgramErrors(t *testing.T) {
	t.Parallel()

	programID := solana.NewWallet().PublicKey()
	customErr := map[string]any{"InstructionError": []any{0, map[string]any{"Custom": 6000}}}

	cases := []struct {
		configure func(*sdktest.FakeRPC)
		name      string
	}{
		{
			configure: func(fakeRPC *sdktest.FakeRPC) {
				fakeRPC.Simulate = func(*solana.Transaction) sdktest.SimulateResult {
					return sdktest.SimulateResult{
						Err: customErr,
						Logs: []string{
							"Program " + programID.String() + " invoke [1]",
							"Program log: AnchorError occurred. Error Code: Unauthorized. Error Number: 6000. " +
								"Error Message: unauthorized.",
							"Program " + programID.String() + " failed: custom program error: 0x1770",
						},
					}
				}
			},
			name: "simulated",
		},
		{
			configure: func(fakeRPC *sdktest.FakeRPC) {
				fakeRPC.LandedErr = customErr
			},
			name: "landed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fakeRPC := sdktest.NewFakeRPC(t)
			tc.configure(fakeRPC)

			signer, err := solana.NewRandomPrivateKey()
			require.NoError(t, err)

			sender := tx.NewSender(fakeRPC.NewClient(), nil, tx.DefaultConfig(), zerolog.Nop())
			sender.ProgramIDs = tx.ProgramIDs{DVPN: programID}
			ixs := []solana.Instruction{solana.NewInstruction(programID, nil, nil)}
			_, err = sender.Send(t.Context(), signer, ixs)
			require.ErrorIs(t, err, dvpn.ErrUnauthorized)
		})
	}
}

func TestWatchWS_SubscribesAtRequestedCommitment(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		notification string
		wantReached  bool
	}{
		{name: "success resolves at the requested commitment", notification: `{"err":null}`, wantReached: true},
		{name: "failure is left to polling", notification: `{"err":{"InstructionError":[0,{"Custom":6000}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			commits := make(chan rpc.CommitmentType, 1)
			upgrader := websocket.Upgrader{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()

				var request struct {
					ID     uint64            `json:"id"`
					Params []json.RawMessage `json:"params"`
				}
				if err := conn.ReadJSON(&request); err != nil {
					return
				}

				var options struct {
					Commitment rpc.CommitmentType `json:"commitment"`
				}

				_ = json.Unmarshal(request.Params[1], &options)
				commits <- options.Commitment

				reply := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":1}`, request.ID)
				notify := `{"jsonrpc":"2.0","method":"signatureNotification","params":{"subscription":1,` +
					`"result":{"context":{"slot":7},"value":` + tc.notification + `}}}`

				_ = conn.WriteMessage(websocket.TextMessage, []byte(reply))
				_ = conn.WriteMessage(websocket.TextMessage, []byte(notify))
			}))
			t.Cleanup(server.Close)

			wsClient, err := ws.Connect(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"))
			require.NoError(t, err)
			t.Cleanup(wsClient.Close)

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			t.Cleanup(cancel)

			reached, failed := tx.WatchWS(ctx, wsClient, rpc.CommitmentProcessed)
			require.Equal(t, tc.wantReached, reached)
			require.False(t, failed)
			require.Equal(t, rpc.CommitmentProcessed, <-commits)
		})
	}
}

func TestWritable_KeepsPayerFirstAndDedupes(t *testing.T) {
	t.Parallel()

	payer := solana.PublicKey{1}
	writableAccount := solana.PublicKey{2}
	readonlyAccount := solana.PublicKey{3}

	accounts := solana.AccountMetaSlice{
		{PublicKey: writableAccount, IsWritable: true},
		{PublicKey: readonlyAccount, IsWritable: false},
		{PublicKey: writableAccount, IsWritable: true},
		{PublicKey: payer, IsWritable: true},
	}
	instruction := solana.NewInstruction(solana.SystemProgramID, accounts, nil)

	ixs := []solana.Instruction{instruction}

	got := tx.Writable(ixs, payer)
	wantGot := solana.PublicKeySlice{payer, writableAccount}
	require.Equal(t, wantGot, got)
}
