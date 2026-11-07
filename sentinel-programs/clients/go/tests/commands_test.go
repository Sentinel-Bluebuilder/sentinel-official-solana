package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sentinel-official/sentinel-programs/clients/go/cli"
	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/sentinel-official/sentinel-programs/clients/go/core/coretest"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/spf13/cobra"
)

type recordingRunner struct {
	accounts map[string]solana.PublicKey
	ix       solana.Instruction
	name     string
	out      any
	rpc      *rpc.Client
	signer   solana.PublicKey
}

func (r *recordingRunner) Account(name string) (solana.PublicKey, bool) {
	k, ok := r.accounts[name]

	return k, ok
}

func (r *recordingRunner) Options() core.Options { return core.Options{} }

func (r *recordingRunner) Print(v any) error {
	r.out = v

	return nil
}

func (r *recordingRunner) ProgramID() solana.PublicKey { return dvpn.ProgramID }

func (r *recordingRunner) RPC() *rpc.Client { return r.rpc }

func (r *recordingRunner) Run(_ context.Context, name string, ix solana.Instruction) error {
	r.name = name
	r.ix = ix

	return nil
}

func (r *recordingRunner) Signer(context.Context) (solana.PublicKey, error) { return r.signer, nil }

func TestCommands_CloseNode(t *testing.T) {
	signer := solana.MustPublicKeyFromBase58("11111111111111111111111111111112")
	mint := solana.MustPublicKeyFromBase58("11111111111111111111111111111113")
	node := solana.MustPublicKeyFromBase58("11111111111111111111111111111116")
	rentRecipient := solana.MustPublicKeyFromBase58("11111111111111111111111111111118")

	var id [32]uint8
	id[0] = 9

	body, err := dvpn.Node{Owner: signer, NodeID: id, RentRecipient: rentRecipient}.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	srv := coretest.New(t)
	srv.SetAccount(node, coretest.Account{Owner: dvpn.ProgramID, Data: append(dvpn.NodeAccountDiscriminator[:], body...)})

	r := &recordingRunner{signer: signer, rpc: srv.Client(), accounts: map[string]solana.PublicKey{"owner_token": signer, "vault": mint, "mint": mint}}

	c := findCommand(t, r, "close-node")
	c.SetArgs([]string{"--node", node.String()})

	if err := c.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	if r.name != "close_node" {
		t.Fatalf("name = %q", r.name)
	}

	if got := r.ix.Accounts()[6].PublicKey; got != rentRecipient {
		t.Fatalf("node rent recipient account = %s, want %s", got, rentRecipient)
	}
}

func TestCommands_OpenChannel(t *testing.T) {
	signer := solana.MustPublicKeyFromBase58("11111111111111111111111111111112")
	owner := solana.MustPublicKeyFromBase58("11111111111111111111111111111113")
	node := solana.MustPublicKeyFromBase58("11111111111111111111111111111114")
	mint := solana.MustPublicKeyFromBase58("11111111111111111111111111111115")

	r := &recordingRunner{signer: signer, accounts: map[string]solana.PublicKey{"mint": mint, "vault": mint}}

	c := findCommand(t, r, "open-channel")
	c.SetArgs([]string{
		"--channel-id", strings.Repeat("01", 32), "--escrow", "9",
		"--terms", "stream", "--terms.stream.expected-rate-per-hour", "7",
		"--owner", owner.String(), "--node", node.String(), "--owner-token", owner.String(),
	})

	if err := c.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	var id [32]uint8
	for i := range id {
		id[i] = 1
	}

	want, err := dvpn.BuildOpenChannelInstruction(dvpn.ProgramID, dvpn.OpenChannelParams{
		ChannelID:            id,
		Escrow:               9,
		Terms:                &dvpn.PositionTermsStream{ExpectedRatePerHour: 7},
		ChannelRentRecipient: signer,
		UserRentRecipient:    signer,
		Signer:               signer,
		Owner:                owner,
		Node:                 node,
		Vault:                mint,
		Mint:                 mint,
		OwnerToken:           owner,
		RentPayer:            signer,
	})
	if err != nil {
		t.Fatal(err)
	}

	requireSameInstruction(t, r.ix, want)
}

func TestCommands_OpenNode(t *testing.T) {
	signer := solana.MustPublicKeyFromBase58("11111111111111111111111111111112")
	mint := solana.MustPublicKeyFromBase58("11111111111111111111111111111113")

	r := &recordingRunner{signer: signer, accounts: map[string]solana.PublicKey{"mint": mint}}

	c := findCommand(t, r, "open-node")
	c.SetArgs([]string{
		"--node-id", strings.Repeat("ab", 32),
		"--prices.channel-price-per-gb", "1", "--prices.channel-price-per-hour", "2", "--prices.lease-price-per-hour", "3",
		"--protocols", "1", "--country", "US", "--uri", "https://node", "--bond", "5",
		"--owner", signer.String(), "--owner-token", signer.String(), "--vault", mint.String(),
		"--remaining-account", mint.String() + ":w",
	})

	if err := c.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	var id [32]uint8
	for i := range id {
		id[i] = 0xab
	}

	want, err := dvpn.BuildOpenNodeInstruction(dvpn.ProgramID, dvpn.OpenNodeParams{
		NodeID:            id,
		Prices:            dvpn.NodePrices{ChannelPricePerGB: 1, ChannelPricePerHour: 2, LeasePricePerHour: 3},
		Protocols:         1,
		Country:           [2]uint8{'U', 'S'},
		URI:               []byte("https://node"),
		Bond:              5,
		NodeRentRecipient: signer,
		Signer:            signer,
		RentPayer:         signer,
		Owner:             signer,
		OwnerToken:        signer,
		Vault:             mint,
		Mint:              mint,
		RemainingAccounts: []*solana.AccountMeta{solana.NewAccountMeta(mint, true, false)},
	})
	if err != nil {
		t.Fatal(err)
	}

	if r.name != "open_node" {
		t.Fatalf("name = %q", r.name)
	}

	requireSameInstruction(t, r.ix, want)

	c = findCommand(t, &recordingRunner{signer: signer}, "open-node")
	c.SetArgs([]string{"--bond", "5"})

	if err := c.ExecuteContext(context.Background()); !errors.Is(err, cli.ErrMissingFlag) {
		t.Fatalf("missing flags: %v", err)
	}
}

func TestCommands_OpenSubscription(t *testing.T) {
	signer := solana.MustPublicKeyFromBase58("11111111111111111111111111111112")
	mint := solana.MustPublicKeyFromBase58("11111111111111111111111111111113")
	vault := solana.MustPublicKeyFromBase58("11111111111111111111111111111114")
	token := solana.MustPublicKeyFromBase58("11111111111111111111111111111115")
	plan := solana.MustPublicKeyFromBase58("11111111111111111111111111111116")
	provider := solana.MustPublicKeyFromBase58("11111111111111111111111111111117")
	given := solana.MustPublicKeyFromBase58("11111111111111111111111111111118")
	rentPayer := solana.MustPublicKeyFromBase58("11111111111111111111111111111119")

	args := []string{
		"--plan", plan.String(), "--provider", provider.String(), "--owner", signer.String(),
		"--expected-price", "1", "--expected-duration-seconds", "2", "--expected-data-cap-gb", "3", "--expected-max-connections", "4",
	}
	base := dvpn.OpenSubscriptionParams{
		ExpectedPrice:             1,
		ExpectedDurationSeconds:   2,
		ExpectedDataCapGB:         3,
		ExpectedMaxConnections:    4,
		SubscriptionRentRecipient: signer,
		Signer:                    signer,
		RentPayer:                 signer,
		Owner:                     signer,
		Plan:                      plan,
		Provider:                  provider,
	}

	answered := &recordingRunner{signer: signer, accounts: map[string]solana.PublicKey{
		"owner_token": token, "vault": vault, "mint": mint, "token_program": solana.Token2022ProgramID,
		"rent_payer": rentPayer,
	}}
	c := findCommand(t, answered, "open-subscription")
	c.SetArgs(args)

	if err := c.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	withDefaults := base
	withDefaults.RentPayer = rentPayer
	withDefaults.OwnerToken = &token
	withDefaults.Vault = &vault
	withDefaults.Mint = &mint
	tp := solana.Token2022ProgramID
	withDefaults.TokenProgram = &tp

	want, err := dvpn.BuildOpenSubscriptionInstruction(dvpn.ProgramID, withDefaults)
	if err != nil {
		t.Fatal(err)
	}

	requireSameInstruction(t, answered.ix, want)

	flagged := &recordingRunner{signer: signer, accounts: map[string]solana.PublicKey{"vault": vault}}
	c = findCommand(t, flagged, "open-subscription")
	c.SetArgs(append(append([]string{}, args...), "--vault", given.String()))

	if err := c.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	overridden := base
	overridden.Vault = &given

	want, err = dvpn.BuildOpenSubscriptionInstruction(dvpn.ProgramID, overridden)
	if err != nil {
		t.Fatal(err)
	}

	requireSameInstruction(t, flagged.ix, want)
}

func TestCommands_SearchNode(t *testing.T) {
	owner1 := solana.MustPublicKeyFromBase58("11111111111111111111111111111112")
	owner2 := solana.MustPublicKeyFromBase58("11111111111111111111111111111113")
	node1 := solana.MustPublicKeyFromBase58("11111111111111111111111111111114")
	node2 := solana.MustPublicKeyFromBase58("11111111111111111111111111111115")

	srv := coretest.New(t)

	for node, owner := range map[solana.PublicKey]solana.PublicKey{node1: owner1, node2: owner2} {
		body, err := dvpn.Node{Owner: owner}.Marshal()
		if err != nil {
			t.Fatal(err)
		}

		srv.SetAccount(node, coretest.Account{Owner: dvpn.ProgramID, Data: append(dvpn.NodeAccountDiscriminator[:], body...)})
	}

	r := &recordingRunner{rpc: srv.Client()}

	c := findCommand(t, r, "search-node")
	c.SetArgs([]string{"--owner", owner1.String()})

	if err := c.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, ok := r.out.([]core.Keyed[dvpn.Node])
	if !ok || len(got) != 1 || got[0].Address != node1 || got[0].Value.Owner != owner1 {
		t.Fatalf("out = %#v", r.out)
	}

	// One command per instruction, plus show- and search- per account type, as the IDL lists them.
	if n, want := len(dvpn.Commands(r)), idlCommandCount(t); n != want {
		t.Fatalf("commands = %d, want %d", n, want)
	}
}

func findCommand(t *testing.T, r cli.Runner, use string) *cobra.Command {
	for _, c := range dvpn.Commands(r) {
		if c.Use == use {
			c.SilenceUsage = true
			c.SilenceErrors = true

			return c
		}
	}

	t.Fatalf("command %s not found", use)

	return nil
}

func requireSameInstruction(t *testing.T, got, want solana.Instruction) {
	t.Helper()

	if got.ProgramID() != want.ProgramID() {
		t.Fatalf("program ID: got %s, want %s", got.ProgramID(), want.ProgramID())
	}

	if !reflect.DeepEqual(got.Accounts(), want.Accounts()) {
		t.Fatalf("accounts: got %+v, want %+v", got.Accounts(), want.Accounts())
	}

	gotData, err := got.Data()
	if err != nil {
		t.Fatal(err)
	}

	wantData, err := want.Data()
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(gotData, wantData) {
		t.Fatalf("data: got %x, want %x", gotData, wantData)
	}
}

// idlCommandCount is how many commands the generated CLI should hold for the dvpn IDL.
func idlCommandCount(t *testing.T) int {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "..", "..", "idl", "dvpn.json"))
	if err != nil {
		t.Fatalf("reading the dvpn IDL: %v", err)
	}

	var idl struct {
		Accounts     []json.RawMessage `json:"accounts"`
		Instructions []json.RawMessage `json:"instructions"`
	}

	if err := json.Unmarshal(data, &idl); err != nil {
		t.Fatalf("decoding the dvpn IDL: %v", err)
	}

	return len(idl.Instructions) + 2*len(idl.Accounts)
}
