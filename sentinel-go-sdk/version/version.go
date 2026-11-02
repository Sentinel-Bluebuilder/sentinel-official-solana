package version

import (
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/solana-foundation/solana-go/v2"
)

const modulePath = "github.com/sentinel-official/sentinel-go-sdk/v3"

//nolint:gochecknoglobals
var (
	Commit    = "unknown"
	Mint      = ""
	Network   = ""
	ProgramID = ""
	Tag       = "unknown"
)

var ErrVersionUnset = errors.New("build identity is not set")

type Info struct {
	Commit    string `json:"commit"`
	Mint      string `json:"mint"`
	Network   string `json:"network"`
	ProgramID string `json:"program_id"`
	Tag       string `json:"tag"`
}

func Current() Info {
	return Info{
		Commit:    Commit,
		Mint:      Mint,
		Network:   Network,
		ProgramID: ProgramID,
		Tag:       Tag,
	}
}

func ParseMint() (solana.PublicKey, error) {
	return parse("mint", Mint)
}

func ParseProgramID() (solana.PublicKey, error) {
	return parse("program id", ProgramID)
}

func SDK() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}

	if info.Main.Path == modulePath {
		return info.Main.Version
	}

	for _, dep := range info.Deps {
		if dep.Path == modulePath {
			return dep.Version
		}
	}

	return ""
}

func parse(name, value string) (solana.PublicKey, error) {
	if value == "" {
		return solana.PublicKey{}, fmt.Errorf("%w: %s", ErrVersionUnset, name)
	}

	key, err := solana.PublicKeyFromBase58(value)
	if err != nil {
		return solana.PublicKey{}, fmt.Errorf("parsing %s %q: %w", name, value, err)
	}

	return key, nil
}
