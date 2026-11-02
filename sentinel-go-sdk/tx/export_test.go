package tx

import (
	"context"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/solana-foundation/solana-go/v2/rpc/ws"
)

var ErrPaymasterFailed = errPaymasterFailed

func Classify(err error) error {
	return classify(err)
}

func WatchWS(ctx context.Context, wsClient *ws.Client, commitment rpc.CommitmentType) (reached, failed bool) {
	out := make(chan statusUpdate, 1)
	watchWS(ctx, wsClient, solana.Signature{}, commitment, out)

	select {
	case update := <-out:
		return update.reached, update.failed
	default:
		return false, false
	}
}

func Writable(ixs []solana.Instruction, payer solana.PublicKey) solana.PublicKeySlice {
	return writable(ixs, payer)
}
