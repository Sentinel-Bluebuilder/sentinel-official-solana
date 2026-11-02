package core_test

import (
	"bytes"
	"testing"

	"github.com/solana-foundation/solana-go/v2"

	"github.com/sentinel-official/sentinel-programs/clients/go/core"
)

type fakeInstruction struct{}

func (fakeInstruction) Accounts() []*solana.AccountMeta { return nil }

func (fakeInstruction) Data() ([]byte, error) { return nil, nil }

func (fakeInstruction) ProgramID() solana.PublicKey { return solana.PublicKey{} }

func TestWithProgramID(t *testing.T) {
	oldProgramID := solana.NewWallet().PublicKey()
	programID := solana.NewWallet().PublicKey()
	first := solana.Meta(solana.NewWallet().PublicKey())
	second := solana.Meta(solana.NewWallet().PublicKey())
	remaining := []*solana.AccountMeta{second}
	ix := solana.NewInstruction(oldProgramID, solana.AccountMetaSlice{first}, []byte{1, 2, 3})

	got := core.WithProgramID(ix, programID, remaining)

	if got == ix {
		t.Fatal("instruction must be copied")
	}
	if ix.ProgramID() != oldProgramID || len(ix.Accounts()) != 1 {
		t.Fatalf("input mutated: program ID %s, accounts %d", ix.ProgramID(), len(ix.Accounts()))
	}
	if got.ProgramID() != programID {
		t.Fatalf("program ID = %s, want %s", got.ProgramID(), programID)
	}
	if accounts := got.Accounts(); len(accounts) != 2 || accounts[0] != first || accounts[1] != second {
		t.Fatalf("accounts = %#v, want original account followed by remaining account", accounts)
	}
	data, err := got.Data()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte{1, 2, 3}) {
		t.Fatalf("data = %v, want [1 2 3]", data)
	}
}

func TestWithProgramID_NonGenericInstruction(t *testing.T) {
	ix := fakeInstruction{}
	remaining := solana.Meta(solana.NewWallet().PublicKey())

	got := core.WithProgramID(
		ix,
		solana.NewWallet().PublicKey(),
		[]*solana.AccountMeta{remaining},
	)

	if got != ix {
		t.Fatalf("instruction = %T, want the original instruction", got)
	}
}
