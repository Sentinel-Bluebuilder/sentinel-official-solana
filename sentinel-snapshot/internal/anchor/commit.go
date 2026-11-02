package anchor

import (
	"bytes"
	"fmt"

	cmttypes "github.com/cometbft/cometbft/types"
)

func VerifyCommit(
	chainID string,
	validators *cmttypes.ValidatorSet,
	header *cmttypes.Header,
	commit *cmttypes.Commit,
) error {
	if commit.Height != header.Height {
		return fmt.Errorf("commit height %d != header height %d", commit.Height, header.Height)
	}

	headerHash := header.Hash()
	if !bytes.Equal(commit.BlockID.Hash, headerHash) {
		return fmt.Errorf("commit BlockID.Hash %x != header H hash %x (commit not over header H)",
			commit.BlockID.Hash, headerHash)
	}

	if !bytes.Equal(validators.Hash(), header.ValidatorsHash) {
		return fmt.Errorf("supplied validator-set hash %x != header H ValidatorsHash %x",
			validators.Hash(), header.ValidatorsHash)
	}

	if err := validators.VerifyCommit(chainID, commit.BlockID, header.Height, commit); err != nil {
		return fmt.Errorf("verify commit: %w", err)
	}

	return nil
}
