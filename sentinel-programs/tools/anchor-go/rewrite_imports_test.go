package main_test

import (
	"strings"
	"testing"

	generator "github.com/sentinel-official/sentinel-programs/tools/anchor-go"
)

func TestRewriteImports(t *testing.T) {
	src := []byte(`package x

import (
	solanago "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	binary "github.com/gagliardetto/binary"
)

var _ = solanago.PublicKey{}
var _ = rpc.Client{}
var _ = binary.Encoder{}
`)

	out, err := generator.RewriteImports(src)
	if err != nil {
		t.Fatal(err)
	}

	got := string(out)
	for _, want := range []string{
		`solanago "github.com/solana-foundation/solana-go/v2"`,
		`"github.com/solana-foundation/solana-go/v2/rpc"`,
		`binary "github.com/gagliardetto/binary"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in:\n%s", want, got)
		}
	}

	if strings.Contains(got, "gagliardetto/solana-go") {
		t.Fatalf("old import left in:\n%s", got)
	}
}

func TestRewriteSolanaAlias(t *testing.T) {
	src := []byte(`package x

import solanago "github.com/solana-foundation/solana-go/v2"

var _ = solanago.PublicKey{}
`)

	out, err := generator.RewriteSolanaAlias(src)
	if err != nil {
		t.Fatal(err)
	}

	got := string(out)
	if strings.Contains(got, "solanago") || !strings.Contains(got, "solana.PublicKey{}") {
		t.Fatalf("alias left in:\n%s", got)
	}
}
