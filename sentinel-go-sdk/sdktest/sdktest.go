package sdktest

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	bin "github.com/gagliardetto/binary"
	"github.com/solana-foundation/solana-go/v2"

	"github.com/sentinel-official/sentinel-go-sdk/v3/version"
)

//nolint:gochecknoglobals
var pins struct {
	mint              string
	mu                sync.Mutex
	previousMint      string
	previousProgramID string
	programID         string
	refs              int
}

type BorshMarshaler interface {
	MarshalWithEncoder(encoder *bin.Encoder) error
}

type FakeEgress struct {
	TeardownErr error
	TornDown    bool
}

func (f *FakeEgress) Teardown(context.Context) error {
	if f.TeardownErr != nil {
		return f.TeardownErr
	}

	if f.TornDown {
		return errors.New("teardown called twice")
	}

	f.TornDown = true

	return nil
}

func EncodeAccount(discriminator [8]byte, value BorshMarshaler) []byte {
	var buf bytes.Buffer

	buf.Write(discriminator[:])

	enc := bin.NewBorshEncoder(&buf)
	if err := value.MarshalWithEncoder(enc); err != nil {
		panic(err)
	}

	return buf.Bytes()
}

func PinBuildIdentity(tb testing.TB, programID, mint solana.PublicKey) {
	tb.Helper()

	programIDString, mintString := programID.String(), mint.String()

	pins.mu.Lock()
	if pins.refs > 0 && (pins.programID != programIDString || pins.mint != mintString) {
		pins.mu.Unlock()
		tb.Fatalf("build identity already pinned to a different program or mint")

		return
	}

	if pins.refs == 0 {
		pins.previousProgramID, pins.previousMint = version.ProgramID, version.Mint
		pins.programID, pins.mint = programIDString, mintString
		version.ProgramID, version.Mint = programIDString, mintString
	}

	pins.refs++
	pins.mu.Unlock()

	tb.Cleanup(func() {
		pins.mu.Lock()
		defer pins.mu.Unlock()

		pins.refs--
		if pins.refs == 0 {
			version.ProgramID, version.Mint = pins.previousProgramID, pins.previousMint
		}
	})
}
