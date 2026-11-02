package anchor_test

import (
	"strings"
	"testing"
	"time"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/anchor"
)

func TestVerifyCommit_SignedCommit(t *testing.T) {
	t.Parallel()

	const chainID = "sentinelhub-2"

	validators, privValidators := cmttypes.RandValidatorSet(4, 10)
	header := &cmttypes.Header{Height: 5_000_000, ValidatorsHash: validators.Hash()}
	headerHash := header.Hash()
	blockID := cmttypes.BlockID{
		Hash:          headerHash,
		PartSetHeader: cmttypes.PartSetHeader{Total: 1, Hash: make([]byte, 32)},
	}

	voteSet := cmttypes.NewVoteSet(chainID, header.Height, 0, cmtproto.PrecommitType, validators)

	commit, err := cmttypes.MakeCommit(blockID, header.Height, 0, voteSet, privValidators, time.Now())
	if err != nil {
		t.Fatalf("MakeCommit: %v", err)
	}

	if err := anchor.VerifyCommit(chainID, validators, header, commit); err != nil {
		t.Fatalf("expected a properly-signed commit to verify, got %v", err)
	}
}

func TestVerifyCommit_ValidatorSetNotBoundToHeader(t *testing.T) {
	t.Parallel()

	validators, _ := cmttypes.RandValidatorSet(4, 10)
	other, _ := cmttypes.RandValidatorSet(4, 10)
	header := &cmttypes.Header{Height: 5_000_000, ValidatorsHash: other.Hash()}
	commit := &cmttypes.Commit{Height: 5_000_000, BlockID: cmttypes.BlockID{Hash: header.Hash()}}

	err := anchor.VerifyCommit("sentinelhub-2", validators, header, commit)
	if err == nil || !strings.Contains(err.Error(), "ValidatorsHash") {
		t.Fatalf("expected validator-set binding rejection, got %v", err)
	}
}
