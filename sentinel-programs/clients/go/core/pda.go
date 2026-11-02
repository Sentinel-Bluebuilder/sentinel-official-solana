package core

import (
	"fmt"

	"github.com/solana-foundation/solana-go/v2"
)

func FindProgramDataAddress(programID solana.PublicKey) (solana.PublicKey, error) {
	address, _, err := solana.FindProgramAddress([][]byte{programID[:]}, solana.BPFLoaderUpgradeableProgramID)
	if err != nil {
		return solana.PublicKey{}, fmt.Errorf("deriving program data address: %w", err)
	}

	return address, nil
}
