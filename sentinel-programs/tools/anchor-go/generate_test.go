package main_test

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	generator "github.com/sentinel-official/sentinel-programs/tools/anchor-go"
)

var upstreamFiles = []string{"accounts.go", "constants.go", "discriminators.go", "doc.go", "events.go", "instructions.go", "program_id.go", "types.go"}

func TestGenerate_Collision(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		account string
		symbol  string
	}{
		{name: "fetch by seeds", account: "RecordBySeeds", symbol: "FetchRecordBySeeds"},
		{name: "fetch batch", account: "RecordBatch", symbol: "FetchRecordBatch"},
		{name: "new client", account: "NewClient", symbol: "NewClient"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doc := fmt.Sprintf(`{
					"address": "11111111111111111111111111111111",
					"metadata": {"name": "example", "version": "0.1.0", "spec": "0.1.0"},
					"instructions": [{"name": "read", "discriminator": [1,0,0,0,0,0,0,0],
						"accounts": [{"name": "record", "pda": {
							"seeds": [{"kind": "const", "value": [1]}]}}], "args": []}],
					"accounts": [
						{"name": "Record", "discriminator": [2,0,0,0,0,0,0,0]},
						{"name": "%[1]s", "discriminator": [3,0,0,0,0,0,0,0]}],
					"types": [
						{"name": "Record", "type": {"kind": "struct", "fields": []}},
					{"name": "%[1]s", "type": {"kind": "struct", "fields": []}}]
			}`, tc.account)
			path := filepath.Join(t.TempDir(), "idl.json")
			if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := generator.Generate(path)
			if err == nil || !strings.Contains(err.Error(), tc.symbol) {
				t.Fatalf("collision error = %v", err)
			}
		})
	}
}

func TestGenerate_FlagHelpPhrases(t *testing.T) {
	t.Parallel()

	doc := `{
		"address": "11111111111111111111111111111111",
		"metadata": {"name": "example", "version": "0.1.0", "spec": "0.1.0"},
		"instructions": [
			{"name": "set_rate", "discriminator": [1,0,0,0,0,0,0,0],
				"accounts": [{"name": "rent_payer", "writable": true}],
				"args": [{"name": "expected_rate_per_hour", "type": "u64"}, {"name": "bech32_address", "type": "string"}]}],
		"accounts": [{"name": "Alpha", "discriminator": [3,0,0,0,0,0,0,0]}],
		"types": [
			{"name": "Alpha", "type": {"kind": "struct", "fields": [{"name": "rent_payer", "type": "pubkey"}]}}]
	}`

	path := filepath.Join(t.TempDir(), "idl.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	files, err := generator.Generate(path)
	if err != nil {
		t.Fatal(err)
	}

	commands := string(files["commands.go"])
	for _, want := range []string{`"expected-rate-per-hour", 0, "expected rate per hour"`, `"bech32-address", "", "bech32 address"`, `"rent-payer", "rent payer"`} {
		if !strings.Contains(commands, want) {
			t.Errorf("missing %q in:\n%s", want, commands)
		}
	}

	for _, stale := range []string{`"expected_rate_per_hour"`} {
		if strings.Contains(commands, stale) {
			t.Errorf("raw snake_case help %s in:\n%s", stale, commands)
		}
	}
}

func TestGenerate_PDANamedAfterAccountType(t *testing.T) {
	t.Parallel()

	// "claim_bitmap" is {99,108,97,105,109,95,98,105,116,109,97,112}; the instruction account is called bitmap.
	doc := `{
		"address": "11111111111111111111111111111111",
		"metadata": {"name": "example", "version": "0.1.0", "spec": "0.1.0"},
		"instructions": [
			{"name": "touch", "discriminator": [1,0,0,0,0,0,0,0],
				"accounts": [
					{"name": "bitmap", "pda": {"seeds": [
						{"kind": "const", "value": [99,108,97,105,109,95,98,105,116,109,97,112]},
						{"kind": "arg", "path": "shard_id"}]}},
					{"name": "record", "pda": {"seeds": [{"kind": "const", "value": [114,101,99,111,114,100]}]}}],
				"args": [{"name": "shard_id", "type": "u16"}]}],
		"accounts": [
			{"name": "ClaimBitmap", "discriminator": [3,0,0,0,0,0,0,0]},
			{"name": "Record", "discriminator": [4,0,0,0,0,0,0,0]}],
		"types": [
			{"name": "ClaimBitmap", "type": {"kind": "struct", "fields": []}},
			{"name": "Record", "type": {"kind": "struct", "fields": []}}]
	}`

	path := filepath.Join(t.TempDir(), "idl.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	files, err := generator.Generate(path)
	if err != nil {
		t.Fatal(err)
	}

	pdas := string(files["pdas.go"])
	for _, want := range []string{"SeedClaimBitmap", "func FindClaimBitmapAddress(", "SeedRecord", "func FindRecordAddress("} {
		if !strings.Contains(pdas, want) {
			t.Fatalf("pdas.go lacks %s:\n%s", want, pdas)
		}
	}

	if strings.Contains(pdas, "FindBitmapAddress") || strings.Contains(pdas, "SeedBitmap") {
		t.Fatalf("pda helpers must not use the field name:\n%s", pdas)
	}

	if fetch := string(files["fetch_pda.go"]); !strings.Contains(fetch, "func FetchClaimBitmapBySeeds(") {
		t.Fatalf("fetch_pda.go lacks FetchClaimBitmapBySeeds:\n%s", fetch)
	}

	if builders := string(files["builders.go"]); !strings.Contains(builders, "FindClaimBitmapAddress(") {
		t.Fatalf("builders.go does not call FindClaimBitmapAddress:\n%s", builders)
	}
}

func TestGenerate_RentRecipientArgDefaultsToSigner(t *testing.T) {
	t.Parallel()

	doc := `{
		"address": "11111111111111111111111111111111",
		"metadata": {"name": "example", "version": "0.1.0", "spec": "0.1.0"},
		"instructions": [
			{"name": "open_thing", "discriminator": [1,0,0,0,0,0,0,0],
				"accounts": [{"name": "owner", "writable": true, "signer": true}],
				"args": [{"name": "thing_rent_recipient", "type": "pubkey"}]},
			{"name": "close_thing", "discriminator": [2,0,0,0,0,0,0,0],
				"accounts": [{"name": "owner", "signer": true}, {"name": "thing", "writable": true}, {"name": "rent_recipient", "writable": true}],
				"args": [{"name": "thing_unbonding_rent_recipient", "type": "pubkey"}]}],
		"accounts": [{"name": "Thing", "discriminator": [3,0,0,0,0,0,0,0]}],
		"types": [
			{"name": "Thing", "type": {"kind": "struct", "fields": [{"name": "rent_recipient", "type": "pubkey"}]}}]
	}`

	path := filepath.Join(t.TempDir(), "idl.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	files, err := generator.Generate(path)
	if err != nil {
		t.Fatal(err)
	}

	commands := string(files["commands.go"])
	for _, want := range []string{
		`&p.ThingRentRecipient, "thing-rent-recipient", "thing rent recipient", "address"`,
		"p.ThingRentRecipient = signer",
		"p.ThingUnbondingRentRecipient = signer",
	} {
		if !strings.Contains(commands, want) {
			t.Errorf("missing %q in:\n%s", want, commands)
		}
	}

	for _, required := range []string{
		`cli.Require(cmd.Flags(), "thing-rent-recipient")`,
		`cli.Require(cmd.Flags(), "thing-unbonding-rent-recipient")`,
	} {
		if strings.Contains(commands, required) {
			t.Errorf("rent recipient argument is required by %s in:\n%s", required, commands)
		}
	}

	_, closeThing, _ := strings.Cut(commands, "func newCloseThingCmd(")
	closeThing, _, _ = strings.Cut(closeThing, "\nfunc ")
	for _, want := range []string{
		`&p.ThingUnbondingRentRecipient, "thing-unbonding-rent-recipient"`,
		`&p.RentRecipient, "rent-recipient"`,
	} {
		if !strings.Contains(closeThing, want) {
			t.Errorf("close_thing lacks %s in:\n%s", want, closeThing)
		}
	}

	if strings.Contains(closeThing, "p.RentRecipient = signer") {
		t.Errorf("close_thing defaults the rent_recipient account to the signer:\n%s", closeThing)
	}
}

func TestGenerate_RentRecipientResolution(t *testing.T) {
	t.Parallel()

	doc := `{
		"address": "11111111111111111111111111111111",
		"metadata": {"name": "example", "version": "0.1.0", "spec": "0.1.0"},
		"instructions": [
			{"name": "close_one", "discriminator": [1,0,0,0,0,0,0,0],
				"accounts": [{"name": "alpha", "writable": true}, {"name": "rent_recipient", "writable": true}], "args": []},
			{"name": "close_beta", "discriminator": [5,0,0,0,0,0,0,0],
				"accounts": [{"name": "alpha", "writable": true}, {"name": "beta", "writable": true}, {"name": "rent_recipient", "writable": true}], "args": []},
			{"name": "close_two", "discriminator": [2,0,0,0,0,0,0,0],
				"accounts": [{"name": "alpha", "writable": true}, {"name": "beta", "writable": true}, {"name": "rent_recipient", "writable": true}], "args": []}],
		"accounts": [
			{"name": "Alpha", "discriminator": [3,0,0,0,0,0,0,0]},
			{"name": "Beta", "discriminator": [4,0,0,0,0,0,0,0]}],
		"types": [
			{"name": "Alpha", "type": {"kind": "struct", "fields": [{"name": "rent_recipient", "type": "pubkey"}]}},
			{"name": "Beta", "type": {"kind": "struct", "fields": [{"name": "rent_recipient", "type": "pubkey"}]}}]
	}`

	path := filepath.Join(t.TempDir(), "idl.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	files, err := generator.Generate(path)
	if err != nil {
		t.Fatal(err)
	}

	resolvers := string(files["resolvers.go"])
	if !strings.Contains(resolvers, "func ResolveCloseOneParams(") || !strings.Contains(resolvers, "p.RentRecipient = alpha.Value.RentRecipient") {
		t.Fatalf("close_one does not resolve the rent recipient from alpha:\n%s", resolvers)
	}

	if !strings.Contains(resolvers, "func ResolveCloseBetaParams(") || !strings.Contains(resolvers, "p.RentRecipient = beta.Value.RentRecipient") {
		t.Fatalf("close_beta does not resolve the rent recipient from beta:\n%s", resolvers)
	}

	if strings.Contains(resolvers, "ResolveCloseTwoParams") {
		t.Fatalf("ambiguous close_two must stay unresolved:\n%s", resolvers)
	}
}

func TestGenerate_SecondarySignerResolvesThroughRunner(t *testing.T) {
	t.Parallel()

	doc := `{
		"address": "11111111111111111111111111111111",
		"metadata": {"name": "example", "version": "0.1.0", "spec": "0.1.0"},
		"instructions": [
			{"name": "open_thing", "discriminator": [1,0,0,0,0,0,0,0],
				"accounts": [{"name": "signer", "signer": true}, {"name": "rent_payer", "writable": true, "signer": true}],
				"args": []}]
	}`

	path := filepath.Join(t.TempDir(), "idl.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	files, err := generator.Generate(path)
	if err != nil {
		t.Fatal(err)
	}

	want := `		if p.RentPayer.IsZero() {
			if k, ok := r.Account("rent_payer"); ok {
				p.RentPayer = k
			} else {
				p.RentPayer = signer
			}
		}`
	if commands := string(files["commands.go"]); !strings.Contains(commands, want) {
		t.Fatalf("rent_payer does not resolve through the runner before falling back to the signer:\n%s", commands)
	}
}

func TestRenderBuilders_NoInstructions(t *testing.T) {
	got, err := generator.RenderBuildersWithoutInstructions(filepath.Join("testdata", "idl.json"))
	if err != nil {
		t.Fatal(err)
	}

	if want := "package example\n\n"; got != want {
		t.Fatalf("renderBuilders() = %q, want %q", got, want)
	}
}

func TestRun_UpstreamFiles(t *testing.T) {
	out := filepath.Join(t.TempDir(), "example")
	if err := generator.Run(filepath.Join("testdata", "idl.json"), out); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o755 {
		t.Fatalf("output directory permissions = %o, want 755", mode)
	}

	want := []byte(generator.GeneratedHeader)

	for _, name := range upstreamFiles {
		src, err := os.ReadFile(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}

		info, err := os.Stat(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o644 {
			t.Fatalf("%s: permissions = %o, want 644", name, mode)
		}

		if !bytes.HasPrefix(src, want) {
			t.Fatalf("%s: first line %q", name, bytes.SplitN(src, []byte("\n"), 2)[0])
		}
	}

	for _, name := range []string{"fetchers.go", "tests_test.go", "idl.json"} {
		if _, err := os.Stat(filepath.Join(out, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s must not be written", name)
		}
	}
}
