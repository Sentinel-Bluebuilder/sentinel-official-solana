package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
)

const maxRPCFilters = 4

var (
	ErrAccountNotFound = errors.New("account not found")
	ErrMissingParam    = errors.New("missing parameter")
	ErrOwnerMismatch   = errors.New("account owned by another program")
)

type Decoder[T any] func([]byte) (*T, error)

type Keyed[T any] struct {
	Address solana.PublicKey
	Slot    uint64
	Value   T
}

type Options struct {
	Commitment rpc.CommitmentType
	Owner      solana.PublicKey
}

func Fetch[T any](
	ctx context.Context,
	rpcClient *rpc.Client,
	address solana.PublicKey,
	opts Options,
	decode Decoder[T],
) (Keyed[T], error) {
	result, err := rpcClient.GetAccountInfoWithOpts(ctx, address, &rpc.GetAccountInfoOpts{
		Commitment: opts.Commitment,
		Encoding:   solana.EncodingBase64,
	})
	if errors.Is(err, rpc.ErrNotFound) {
		return Keyed[T]{}, fmt.Errorf("%w: %s", ErrAccountNotFound, address)
	}

	if err != nil {
		return Keyed[T]{}, err
	}

	v, err := decodeAccount(address, result.Value, opts, decode)
	if err != nil {
		return Keyed[T]{}, err
	}

	return Keyed[T]{
		Address: address,
		Slot:    result.Context.Slot,
		Value:   *v,
	}, nil
}

func FetchBatch[T any](
	ctx context.Context,
	rpcClient *rpc.Client,
	addresses []solana.PublicKey,
	opts Options,
	decode Decoder[T],
) ([]Keyed[T], error) {
	if len(addresses) == 0 {
		return nil, nil
	}

	result, err := rpcClient.GetMultipleAccountsWithOpts(ctx, addresses, &rpc.GetMultipleAccountsOpts{
		Commitment: opts.Commitment,
		Encoding:   solana.EncodingBase64,
	})
	if err != nil {
		return nil, err
	}

	if len(result.Value) != len(addresses) {
		return nil, fmt.Errorf("expected %d accounts, got %d", len(addresses), len(result.Value))
	}

	out := make([]Keyed[T], 0, len(addresses))

	for i, accountInfo := range result.Value {
		v, err := decodeAccount(addresses[i], accountInfo, opts, decode)
		if err != nil {
			return nil, err
		}

		out = append(out, Keyed[T]{
			Address: addresses[i],
			Slot:    result.Context.Slot,
			Value:   *v,
		})
	}

	return out, nil
}

func FindAccounts[T any](
	ctx context.Context,
	rpcClient *rpc.Client,
	programID solana.PublicKey,
	opts Options,
	filters []rpc.RPCFilter,
	decode Decoder[T],
) ([]Keyed[T], error) {
	wire := make([]rpc.RPCFilter, 0, maxRPCFilters)
	local := make([]rpc.RPCFilter, 0, len(filters))
	for _, filter := range filters {
		if filter.DataSize == 0 && len(wire) < maxRPCFilters {
			wire = append(wire, filter)
		} else {
			local = append(local, filter)
		}
	}

	result, err := rpcClient.GetProgramAccountsWithContext(ctx, programID, &rpc.GetProgramAccountsOpts{
		Commitment: opts.Commitment,
		Encoding:   solana.EncodingBase64,
		Filters:    wire,
	})
	if err != nil {
		return nil, err
	}

	out := make([]Keyed[T], 0, len(result.Value))

	for _, ka := range result.Value {
		data := ka.Account.Data.GetBinary()
		if !matchesFilters(data, local) {
			continue
		}

		v, err := decode(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ka.Pubkey, err)
		}

		out = append(out, Keyed[T]{
			Address: ka.Pubkey,
			Slot:    result.Context.Slot,
			Value:   *v,
		})
	}

	return out, nil
}

func decodeAccount[T any](
	address solana.PublicKey,
	a *rpc.Account,
	opts Options,
	decode Decoder[T],
) (*T, error) {
	if a == nil {
		return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, address)
	}

	if !opts.Owner.IsZero() && !a.Owner.Equals(opts.Owner) {
		return nil, fmt.Errorf("%w: %s", ErrOwnerMismatch, address)
	}

	v, err := decode(a.Data.GetBinary())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", address, err)
	}

	return v, nil
}

func matchesFilters(data []byte, filters []rpc.RPCFilter) bool {
	for _, filter := range filters {
		if filter.DataSize != 0 && uint64(len(data)) != filter.DataSize {
			return false
		}

		if filter.Memcmp != nil {
			if filter.Memcmp.Offset > uint64(len(data)) || uint64(len(filter.Memcmp.Bytes)) > uint64(len(data))-filter.Memcmp.Offset {
				return false
			}

			end := filter.Memcmp.Offset + uint64(len(filter.Memcmp.Bytes))
			if !bytes.Equal(data[filter.Memcmp.Offset:end], filter.Memcmp.Bytes) {
				return false
			}
		}
	}

	return true
}
