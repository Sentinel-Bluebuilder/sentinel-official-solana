package tx_test

import (
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/programs/system"
)

func selfTransferIxs(signer solana.PrivateKey) []solana.Instruction {
	instruction := system.NewTransferInstruction(1, signer.PublicKey(), signer.PublicKey()).Build()

	return []solana.Instruction{instruction}
}

func transferIxs(from, recipient solana.PublicKey) []solana.Instruction {
	instruction := system.NewTransferInstruction(1_000_000, from, recipient).Build()

	return []solana.Instruction{instruction}
}
