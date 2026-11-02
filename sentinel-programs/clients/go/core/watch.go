package core

import (
	"context"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc/ws"
)

func Watch[T any](
	ctx context.Context,
	wsClient *ws.Client,
	address solana.PublicKey,
	opts Options,
	decode Decoder[T],
) (<-chan Keyed[T], error) {
	sub, err := wsClient.AccountSubscribeWithConfig(address, &ws.AccountSubscribeConfig{
		Commitment: opts.Commitment,
		Encoding:   solana.EncodingBase64,
	})
	if err != nil {
		return nil, err
	}

	out := make(chan Keyed[T])

	go func() {
		defer close(out)
		defer sub.Unsubscribe()

		for {
			result, err := sub.Recv(ctx)
			if err != nil {
				return
			}

			v, err := decodeAccount(address, result.Value, opts, decode)
			if err != nil {
				continue
			}

			select {
			case out <- Keyed[T]{
				Address: address,
				Slot:    result.Context.Slot,
				Value:   *v,
			}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}
