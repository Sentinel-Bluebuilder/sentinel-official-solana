package cli_test

import (
	"testing"

	"github.com/sentinel-official/sentinel-programs/clients/go/cli"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/spf13/pflag"
)

func TestAnyChangedWithPrefix(t *testing.T) {
	var amounts []uint64

	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	cli.SliceVar(fs, &amounts, "amounts", "", "uint64", cli.ParseUint[uint64])

	if err := fs.Parse([]string{"--amounts", "1"}); err != nil {
		t.Fatal(err)
	}

	if !cli.AnyChangedWithPrefix(fs, "amo") || cli.AnyChangedWithPrefix(fs, "other") {
		t.Fatal("Changed must match by prefix")
	}
}

func TestFixedBytesVar(t *testing.T) {
	var id [4]uint8

	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	cli.FixedBytesVar(fs, id[:], "id", "")

	if err := fs.Parse([]string{"--id", "abcd"}); err != nil || string(id[:]) != "abcd" {
		t.Fatalf("text: %v %q", err, id)
	}

	if err := fs.Parse([]string{"--id", "0102030405"}); err == nil || string(id[:]) != "abcd" {
		t.Fatalf("length must be checked and nothing written: %v %q", err, id)
	}

	if err := fs.Parse([]string{"--id", "01020304"}); err != nil || id != [4]uint8{1, 2, 3, 4} {
		t.Fatalf("hex: %v %v", err, id)
	}
}

func TestOptionalVar(t *testing.T) {
	var opt *uint32

	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	cli.OptionalVar(fs, &opt, "opt", "", "uint32", cli.ParseUint[uint32])

	if err := fs.Parse(nil); err != nil || opt != nil {
		t.Fatalf("unset optional: %v %v", err, opt)
	}

	if err := fs.Parse([]string{"--opt", "7"}); err != nil || opt == nil || *opt != 7 {
		t.Fatalf("set optional: %v %v", err, opt)
	}
}

func TestSliceVar(t *testing.T) {
	var amounts []uint64

	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	cli.SliceVar(fs, &amounts, "amounts", "", "uint64", cli.ParseUint[uint64])

	if err := fs.Parse([]string{"--amounts", "1", "--amounts", "2"}); err != nil || len(amounts) != 2 || amounts[1] != 2 {
		t.Fatalf("slice: %v %v", amounts, err)
	}

	if err := fs.Parse([]string{"--amounts", "3,4", "--amounts", "5"}); err != nil || len(amounts) != 5 || amounts[2] != 3 || amounts[3] != 4 || amounts[4] != 5 {
		t.Fatalf("comma-separated slice: %v %v", amounts, err)
	}

	for _, input := range []string{"", "1,,2"} {
		amounts := []uint64{7}
		fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
		cli.SliceVar(fs, &amounts, "amounts", "", "uint64", cli.ParseUint[uint64])

		if err := fs.Parse([]string{"--amounts", input}); err == nil || len(amounts) != 1 || amounts[0] != 7 {
			t.Fatalf("invalid comma-separated slice %q: %v %v", input, amounts, err)
		}
	}
}

func TestVar(t *testing.T) {
	var publicKey solana.PublicKey

	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	cli.Var(fs, &publicKey, "key", "", "public-key", cli.ParsePublicKey)

	if err := fs.Parse([]string{"--key", "11111111111111111111111111111112"}); err != nil {
		t.Fatal(err)
	}

	if publicKey.String() != "11111111111111111111111111111112" {
		t.Fatalf("public key %s", publicKey)
	}
}
