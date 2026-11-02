package tx

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/solana-foundation/solana-go/v2/rpc/ws"

	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/sentinel-official/sentinel-programs/clients/go/distributor"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const (
	landedLogAttempts   = 3
	landedLogInterval   = 500 * time.Millisecond
	pollInterval        = 500 * time.Millisecond
	rebroadcastInterval = 2 * time.Second
)

type FailedOnChainError struct {
	Err                error
	PriorIndeterminate bool
}

func (e *FailedOnChainError) Error() string {
	return e.Err.Error()
}

func (e *FailedOnChainError) Unwrap() error {
	return e.Err
}

type IndeterminateError struct {
	Signature solana.Signature
}

func (e *IndeterminateError) Error() string {
	return fmt.Sprintf("transaction %s outcome is indeterminate", e.Signature)
}

func (e *IndeterminateError) Is(target error) bool {
	return target == ErrOutcomeIndeterminate
}

type UnsentError struct {
	Err error
}

func (e *UnsentError) Error() string {
	return e.Err.Error()
}

func (e *UnsentError) Unwrap() error {
	return e.Err
}

type statusUpdate struct {
	chainErr any
	err      error
	failed   bool
	reached  bool
	slot     uint64
}

func awaitSignature(
	ctx context.Context,
	wsClient *ws.Client,
	sig solana.Signature,
	commitment rpc.CommitmentType,
) (*ws.SignatureResult, error) {
	sub, err := wsClient.SignatureSubscribe(sig, commitment)
	if err != nil {
		return nil, fmt.Errorf("subscribing to signature: %w", err)
	}

	defer sub.Unsubscribe()

	resp, err := sub.Recv(ctx)
	if err != nil {
		return nil, fmt.Errorf("receiving signature notification: %w", err)
	}

	return resp, nil
}

func confirm(
	ctx context.Context,
	rpcClient *rpc.Client,
	wsClient *ws.Client,
	signed *signedTx,
	cfg Config,
) (uint64, error) {
	ctx, cancel := context.WithTimeout(ctx, cfg.ConfirmTimeout)
	defer cancel()

	sig := signed.tx.Signatures[0]
	updates := make(chan statusUpdate, 1)

	watch := func() {
		watchStatus(ctx, rpcClient, wsClient, sig, cfg.Commitment, updates)
	}
	go watch()

	poll := time.NewTicker(pollInterval)
	defer poll.Stop()

	rebroadcastAt := time.Now().Add(rebroadcastInterval)

	for {
		select {
		case <-ctx.Done():
			return 0, &IndeterminateError{Signature: sig}
		case update := <-updates:
			switch {
			case update.err != nil:
				return 0, update.err
			case update.failed:
				return 0, &FailedOnChainError{Err: fetchProgramError(ctx, rpcClient, signed, update.chainErr)}
			case update.reached:
				return update.slot, nil
			}
		case <-poll.C:
			height, err := rpcClient.GetBlockHeight(ctx, cfg.Commitment)
			if err != nil {
				continue
			}

			if height > signed.lastValidBlockHeight {
				final, ferr := signatureStatus(ctx, rpcClient, sig, cfg.Commitment, true)
				switch {
				case ferr == nil && final.reached:
					return final.slot, nil
				case ferr == nil && final.failed:
					return 0, &FailedOnChainError{Err: fetchProgramError(ctx, rpcClient, signed, final.chainErr)}
				default:
					return 0, ErrBlockhashExpired
				}
			}

			if time.Now().After(rebroadcastAt) {
				sendOpts := rpc.TransactionOpts{
					PreflightCommitment: cfg.Commitment,
					SkipPreflight:       true,
				}
				_, _ = rpcClient.SendTransactionWithOpts(ctx, signed.tx, sendOpts)
				rebroadcastAt = time.Now().Add(rebroadcastInterval)
			}
		}
	}
}

func deliver(ctx context.Context, out chan<- statusUpdate, update statusUpdate) {
	select {
	case out <- update:
	case <-ctx.Done():
	}
}

func failureCommitment(want rpc.CommitmentType) rpc.CommitmentType {
	if want == rpc.CommitmentFinalized {
		return rpc.CommitmentFinalized
	}

	return rpc.CommitmentConfirmed
}

func fetchLandedLogs(
	ctx context.Context,
	rpcClient *rpc.Client,
	sig solana.Signature,
	commitment rpc.CommitmentType,
) ([]string, error) {
	opts := transactionOpts(commitment)
	for attempt := range landedLogAttempts {
		resp, err := rpcClient.GetTransaction(ctx, sig, opts)
		if err == nil && resp != nil {
			if resp.Meta == nil {
				return nil, fmt.Errorf("transaction %s has no metadata", sig)
			}

			return resp.Meta.LogMessages, nil
		}

		if err != nil && !errors.Is(err, rpc.ErrNotFound) {
			return nil, fmt.Errorf("fetching landed transaction logs: %w", err)
		}

		if attempt == landedLogAttempts-1 {
			break
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for landed transaction logs: %w", ctx.Err())
		case <-time.After(landedLogInterval):
		}
	}

	return nil, fmt.Errorf("transaction %s not available: %w", sig, rpc.ErrNotFound)
}

func fetchProgramError(ctx context.Context, rpcClient *rpc.Client, signed *signedTx, chainErr any) error {
	sig := signed.tx.Signatures[0]

	resp, err := rpcClient.GetTransaction(ctx, sig, transactionOpts(rpc.CommitmentConfirmed))
	if err == nil && resp != nil && resp.Meta != nil {
		decoded := core.DecodeTxError(resp.Meta.Err, resp.Meta.LogMessages, signed.programs)
		if decoded != nil {
			return programError(decoded, signed.errorPrograms)
		}
	}

	if err == nil && resp == nil {
		err = rpc.ErrNotFound
	}

	if decoded := core.DecodeTxError(chainErr, nil, signed.programs); decoded != nil {
		programErr := programError(decoded, signed.errorPrograms)
		if err != nil {
			return errors.Join(programErr, err)
		}

		return programErr
	}

	if err != nil {
		return fmt.Errorf("transaction %s failed on chain: %w", sig, err)
	}

	return fmt.Errorf("transaction %s failed on chain", sig)
}

func programError(decoded *core.TxError, ids ProgramIDs) error {
	switch decoded.Program {
	case cmp.Or(ids.DVPN, dvpn.ProgramID).String():
		if known, ok := dvpn.ErrorByCode(decoded.Code); ok {
			return fmt.Errorf("program error: %w", known)
		}
	case cmp.Or(ids.Distributor, distributor.ProgramID).String():
		if known, ok := distributor.ErrorByCode(decoded.Code); ok && decoded.Name == "" {
			return fmt.Errorf("program error: %w", known)
		}
	}

	return decoded
}

func reached(status rpc.ConfirmationStatusType, want rpc.CommitmentType) bool {
	switch want {
	case rpc.CommitmentFinalized:
		return status == rpc.ConfirmationStatusFinalized
	case rpc.CommitmentConfirmed:
		return status == rpc.ConfirmationStatusConfirmed || status == rpc.ConfirmationStatusFinalized
	case rpc.CommitmentProcessed:
		return status != ""
	default:
		return status != ""
	}
}

func signatureStatus(
	ctx context.Context,
	rpcClient *rpc.Client,
	sig solana.Signature,
	commitment rpc.CommitmentType,
	searchHistory bool,
) (statusUpdate, error) {
	resp, err := rpcClient.GetSignatureStatuses(ctx, searchHistory, sig)
	if err != nil {
		return statusUpdate{}, classify(err)
	}

	if len(resp.Value) == 0 || resp.Value[0] == nil {
		return statusUpdate{}, nil
	}

	status := resp.Value[0]
	switch {
	case status.Err != nil && reached(status.ConfirmationStatus, failureCommitment(commitment)):
		update := statusUpdate{
			chainErr: status.Err,
			failed:   true,
			slot:     status.Slot,
		}

		return update, nil
	case status.Err != nil:
		return statusUpdate{}, nil
	case reached(status.ConfirmationStatus, commitment):
		update := statusUpdate{
			reached: true,
			slot:    status.Slot,
		}

		return update, nil
	default:
		return statusUpdate{}, nil
	}
}

func transactionOpts(commitment rpc.CommitmentType) *rpc.GetTransactionOpts {
	version := rpc.MaxSupportedTransactionVersion1

	if commitment == rpc.CommitmentProcessed {
		commitment = rpc.CommitmentConfirmed
	}

	return &rpc.GetTransactionOpts{
		Commitment:                     commitment,
		Encoding:                       solana.EncodingBase64,
		MaxSupportedTransactionVersion: &version,
	}
}

func watchPoll(
	ctx context.Context,
	rpcClient *rpc.Client,
	sig solana.Signature,
	commitment rpc.CommitmentType,
	out chan<- statusUpdate,
) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		update, err := signatureStatus(ctx, rpcClient, sig, commitment, false)
		switch {
		case err != nil && !errors.Is(err, ErrRPCTransient):
			update.err = err
			deliver(ctx, out, update)

			return
		case update.failed || update.reached:
			deliver(ctx, out, update)

			return
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func watchStatus(
	ctx context.Context,
	rpcClient *rpc.Client,
	wsClient *ws.Client,
	sig solana.Signature,
	commitment rpc.CommitmentType,
	out chan<- statusUpdate,
) {
	if wsClient != nil {
		subscribe := func() {
			watchWS(ctx, wsClient, sig, commitment, out)
		}
		go subscribe()
	}

	watchPoll(ctx, rpcClient, sig, commitment, out)
}

func watchWS(
	ctx context.Context,
	wsClient *ws.Client,
	sig solana.Signature,
	commitment rpc.CommitmentType,
	out chan<- statusUpdate,
) {
	resp, err := awaitSignature(ctx, wsClient, sig, commitment)
	if err != nil || resp.Value.Processed.Err != nil {
		return
	}

	update := statusUpdate{
		reached: true,
		slot:    resp.Context.Slot,
	}
	deliver(ctx, out, update)
}
