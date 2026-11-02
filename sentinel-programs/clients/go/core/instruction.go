package core

import (
	"slices"

	"github.com/solana-foundation/solana-go/v2"
)

// WithProgramID returns a copy of ix addressed to programID with the remaining accounts appended. Instructions
// other than *solana.GenericInstruction are returned unchanged.
func WithProgramID(
	ix solana.Instruction,
	programID solana.PublicKey,
	remainingAccounts []*solana.AccountMeta,
) solana.Instruction {
	generic, ok := ix.(*solana.GenericInstruction)
	if !ok {
		return ix
	}

	out := *generic
	out.ProgID = programID
	out.AccountValues = append(slices.Clone(generic.AccountValues), remainingAccounts...)

	return &out
}
