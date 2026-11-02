package anchor_test

import (
	"bytes"
	"testing"
	"time"

	cmttypes "github.com/cometbft/cometbft/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/anchor"
)

func TestFromHeader(t *testing.T) {
	t.Parallel()

	appHash := bytes.Repeat([]byte{0xAB}, 32)
	ts := time.Unix(1_700_000_123, 999_999_999).UTC()
	h := &cmttypes.Header{Height: 5_000_000, Time: ts, AppHash: appHash}

	got := anchor.FromHeader(h)
	if got.Height != 5_000_000 {
		t.Fatalf("Height = %d", got.Height)
	}

	if !bytes.Equal(got.AppHash, appHash) {
		t.Fatal("AppHash mismatch")
	}

	if got.BlockTimeUnix() != 1_700_000_123 {
		t.Fatalf("BlockTimeUnix = %d, want floored 1700000123", got.BlockTimeUnix())
	}

	if got.StateVersion() != 4_999_999 {
		t.Fatalf("StateVersion = %d, want H-1", got.StateVersion())
	}
}
