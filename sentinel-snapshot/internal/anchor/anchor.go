package anchor

import (
	"fmt"
	"os"
	"path/filepath"

	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmttypes "github.com/cometbft/cometbft/types"
)

type Halt struct {
	Height        int64
	AppHash       []byte
	blockTimeUnix int64
}

func (h Halt) BlockTimeUnix() int64 { return h.blockTimeUnix }

func (h Halt) StateVersion() int64 { return h.Height - 1 }

func FromHeader(h *cmttypes.Header) Halt {
	return Halt{
		Height:        h.Height,
		AppHash:       append([]byte(nil), h.AppHash...),
		blockTimeUnix: h.Time.Unix(),
	}
}

func LoadCommit(path string) (*cmttypes.Commit, error) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read commit %s: %w", path, err)
	}

	var commit cmttypes.Commit
	if err := cmtjson.Unmarshal(b, &commit); err != nil {
		return nil, fmt.Errorf("load commit %s: %w", path, err)
	}

	return &commit, nil
}

func LoadHeader(path string) (*cmttypes.Header, error) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read header %s: %w", path, err)
	}

	var header cmttypes.Header
	if err := cmtjson.Unmarshal(b, &header); err != nil {
		return nil, fmt.Errorf("load header %s: %w", path, err)
	}

	return &header, nil
}

func LoadValidatorSet(path string) (*cmttypes.ValidatorSet, error) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read validator set %s: %w", path, err)
	}

	var validators cmttypes.ValidatorSet
	if err := cmtjson.Unmarshal(b, &validators); err != nil {
		return nil, fmt.Errorf("load validator set %s: %w", path, err)
	}

	return &validators, nil
}
