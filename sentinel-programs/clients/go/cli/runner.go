package cli

import (
	"context"

	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
)

type Runner interface {
	Signer(ctx context.Context) (solana.PublicKey, error)
	RPC() *rpc.Client
	Options() core.Options
	ProgramID() solana.PublicKey
	Account(name string) (solana.PublicKey, bool)
	Run(ctx context.Context, name string, ix solana.Instruction) error
	Print(v any) error
}
