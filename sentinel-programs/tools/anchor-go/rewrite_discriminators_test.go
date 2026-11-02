package main_test

import (
	"strings"
	"testing"

	generator "github.com/sentinel-official/sentinel-programs/tools/anchor-go"
)

func TestRewriteDiscriminators(t *testing.T) {
	src := []byte(`package x

// Builds a "accept_authority" instruction.
func newAcceptAuthorityInstruction(a solanago.PublicKey) (solanago.Instruction, error) {
	accounts__ := solanago.AccountMetaSlice{}
	return solanago.NewInstruction(
		ProgramID,
		accounts__,
		nil,
	), nil
}

// Builds a "add_operator" instruction.
func newAddOperatorInstruction(a solanago.PublicKey) (solanago.Instruction, error) {
	accounts__ := solanago.AccountMetaSlice{}
	return solanago.NewInstruction(ProgramID, accounts__, buf__.Bytes()), nil
}
`)

	out, err := generator.RewriteDiscriminators(src)
	if err != nil {
		t.Fatal(err)
	}

	got := string(out)
	if !strings.Contains(got, "\t\tAcceptAuthorityInstructionDiscriminator[:],\n\t), nil") {
		t.Fatalf("nil data not replaced in place in:\n%s", got)
	}

	if !strings.Contains(got, "}\n\n// Builds a \"add_operator\" instruction.\nfunc newAddOperatorInstruction") {
		t.Fatalf("doc comment displaced in:\n%s", got)
	}

	if !strings.Contains(got, "solanago.NewInstruction(ProgramID, accounts__, buf__.Bytes())") {
		t.Fatalf("non-nil data changed in:\n%s", got)
	}
}
