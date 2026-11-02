package tests

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/sentinel-official/sentinel-programs/clients/go/core/coretest"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc/jsonrpc"
)

func TestBuildAcceptAuthorityInstruction(t *testing.T) {
	authority := solana.MustPublicKeyFromBase58("11111111111111111111111111111112")
	config, _, _ := dvpn.FindConfigAddress(dvpn.ProgramID)

	got, err := dvpn.BuildAcceptAuthorityInstruction(dvpn.ProgramID, dvpn.AcceptAuthorityParams{Authority: authority})
	if err != nil {
		t.Fatal(err)
	}

	accounts := got.Accounts()
	if len(accounts) != 2 || accounts[0].PublicKey != authority || !accounts[0].IsSigner || accounts[1].PublicKey != config {
		t.Fatalf("accounts = %+v", accounts)
	}

	data, err := got.Data()
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(data, dvpn.AcceptAuthorityInstructionDiscriminator[:]) {
		t.Fatalf("data = %x", data)
	}

	other := solana.MustPublicKeyFromBase58("11111111111111111111111111111113")

	closeIx, err := dvpn.BuildCloseChannelInstruction(other, dvpn.CloseChannelParams{Signer: authority, Channel: authority, User: authority, RentRecipient: authority})
	if err != nil {
		t.Fatal(err)
	}

	if closeIx.ProgramID() != other {
		t.Fatalf("ProgramID = %s", closeIx.ProgramID())
	}

	if node := closeIx.Accounts()[5].PublicKey; node != other {
		t.Fatalf("omitted optional node = %s", node)
	}
}

func TestBuildAcceptAuthorityInstruction_RemainingAccounts(t *testing.T) {
	authority := solana.MustPublicKeyFromBase58("11111111111111111111111111111112")
	extra := solana.MustPublicKeyFromBase58("11111111111111111111111111111113")

	metas := []*solana.AccountMeta{solana.NewAccountMeta(extra, true, false), solana.NewAccountMeta(authority, false, false)}

	ix, err := dvpn.BuildAcceptAuthorityInstruction(dvpn.ProgramID, dvpn.AcceptAuthorityParams{Authority: authority, RemainingAccounts: metas})
	if err != nil {
		t.Fatal(err)
	}

	accounts := ix.Accounts()
	if len(accounts) != 4 || !reflect.DeepEqual(accounts[2:], metas) {
		t.Fatalf("accounts = %+v", accounts)
	}
}

func TestBuildCloseNodeInstructionOnChain(t *testing.T) {
	owner := solana.MustPublicKeyFromBase58("11111111111111111111111111111112")
	node := solana.MustPublicKeyFromBase58("11111111111111111111111111111116")

	body, err := dvpn.Node{Owner: owner}.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	srv := coretest.New(t)
	srv.SetAccount(node, coretest.Account{Owner: dvpn.ProgramID, Data: append(dvpn.NodeAccountDiscriminator[:], body...)})

	params := dvpn.CloseNodeParams{Signer: owner, RentPayer: owner, Node: node, RentRecipient: owner, OwnerToken: owner, Vault: owner, Mint: owner}

	got, err := dvpn.BuildCloseNodeInstructionOnChain(context.Background(), srv.Client(), core.Options{}, dvpn.ProgramID, params)
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := dvpn.ResolveCloseNodeParams(context.Background(), srv.Client(), core.Options{}, dvpn.ProgramID, params)
	if err != nil {
		t.Fatal(err)
	}

	want, err := dvpn.BuildCloseNodeInstruction(dvpn.ProgramID, resolved)
	if err != nil {
		t.Fatal(err)
	}

	if got.ProgramID() != want.ProgramID() {
		t.Fatalf("program ID: got %s, want %s", got.ProgramID(), want.ProgramID())
	}

	if !reflect.DeepEqual(got.Accounts(), want.Accounts()) {
		t.Fatalf("accounts\n got %+v\nwant %+v", got.Accounts(), want.Accounts())
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

func TestChannelOwnerFilter(t *testing.T) {
	if dvpn.ChannelAccountSizeBytes != 299 {
		t.Fatalf("ChannelAccountSizeBytes = %d", dvpn.ChannelAccountSizeBytes)
	}

	if f := dvpn.ChannelFilter(); f.Memcmp == nil || f.Memcmp.Offset != 0 || !bytes.Equal(f.Memcmp.Bytes, dvpn.ChannelAccountDiscriminator[:]) {
		t.Fatalf("ChannelFilter = %+v", f)
	}

	owner := solana.MustPublicKeyFromBase58("11111111111111111111111111111112")

	if f := dvpn.ChannelOwnerFilter(owner); f.Memcmp == nil || f.Memcmp.Offset != 40 || !bytes.Equal(f.Memcmp.Bytes, owner[:]) {
		t.Fatalf("ChannelOwnerFilter = %+v", f)
	}
}

func TestErrorByCode(t *testing.T) {
	if e, ok := dvpn.ErrorByCode(6000); !ok || e != dvpn.ErrUnauthorized {
		t.Fatalf("ErrorByCode(6000) = %v, %v", e, ok)
	}

	if got := dvpn.ErrUnauthorized.Error(); got != "Unauthorized (6000): signer is not authorized for this action" {
		t.Fatalf("Error() = %q", got)
	}
}

func TestFetchChannel(t *testing.T) {
	data := make([]byte, dvpn.ChannelAccountSizeBytes)
	copy(data, dvpn.ChannelAccountDiscriminator[:])
	data[8] = 7

	address := solana.MustPublicKeyFromBase58("11111111111111111111111111111115")

	srv := coretest.New(t)
	srv.SetAccount(address, coretest.Account{Owner: dvpn.ProgramID, Data: data})
	srv.SetSlot(3)

	ch, err := dvpn.FetchChannel(context.Background(), srv.Client(), address, core.Options{Owner: dvpn.ProgramID})
	if err != nil {
		t.Fatal(err)
	}

	if ch.Address != address || ch.Slot != 3 || ch.Value.ChannelID[0] != 7 {
		t.Fatalf("got %+v", ch)
	}

	if _, err := dvpn.FetchChannel(context.Background(), srv.Client(), dvpn.ProgramID, core.Options{}); !errors.Is(err, core.ErrAccountNotFound) {
		t.Fatalf("missing account: %v", err)
	}
}

func TestFindChannelAddress(t *testing.T) {
	config, bump, err := dvpn.FindConfigAddress(dvpn.ProgramID)
	if err != nil {
		t.Fatal(err)
	}

	if config.String() != "5CwcUcMgZT4XCHLnzGiDtcYJPBbmqJQRPQRSZytSfTJA" || bump != 255 {
		t.Fatalf("config = %s %d", config, bump)
	}

	owner := solana.MustPublicKeyFromBase58("11111111111111111111111111111112")

	var id [32]uint8
	id[0] = 1

	channel, bump, err := dvpn.FindChannelAddress(dvpn.ProgramID, owner, id)
	if err != nil {
		t.Fatal(err)
	}

	if channel.String() != "5nSruYbYYLzadYF7AksMLm9yGo24U5ZVrmW9j1ghG38Y" || bump != 254 {
		t.Fatalf("channel = %s %d", channel, bump)
	}

	if dvpn.SeedChannel != "channel" {
		t.Fatalf("SeedChannel = %q", dvpn.SeedChannel)
	}
}

func TestFixedAddresses(t *testing.T) {
	if dvpn.TokenProgramAddress.String() != "TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb" {
		t.Fatalf("TokenProgramAddress = %s", dvpn.TokenProgramAddress)
	}

	if dvpn.SystemProgramAddress != solana.SystemProgramID {
		t.Fatalf("SystemProgramAddress = %s", dvpn.SystemProgramAddress)
	}
}

func TestInstructionName(t *testing.T) {
	if name, ok := dvpn.InstructionName(dvpn.OpenNodeInstructionDiscriminator); !ok || name != "open_node" {
		t.Fatalf("InstructionName = %q %v", name, ok)
	}

	if name, ok := dvpn.AccountName(dvpn.NodeAccountDiscriminator); !ok || name != "Node" {
		t.Fatalf("AccountName = %q %v", name, ok)
	}

	if name, ok := dvpn.EventName(dvpn.NodeOpenedEventDiscriminator); !ok || name != "NodeOpened" {
		t.Fatalf("EventName = %q %v", name, ok)
	}

	if _, ok := dvpn.EventName([8]byte{}); ok {
		t.Fatal("unknown discriminator must not resolve")
	}
}

func TestParseError(t *testing.T) {
	rpcErr := &jsonrpc.RPCError{Code: -32002, Message: "failed", Data: map[string]any{"err": instructionErrorFixture("6014")}}

	e, ok := dvpn.ErrorFromRPCError(fmt.Errorf("send: %w", rpcErr))
	if !ok || e != dvpn.ErrURITooLong {
		t.Fatalf("ErrorFromRPCError = %v, %v", e, ok)
	}

	if _, ok := dvpn.ErrorFromValue(instructionErrorFixture("3012")); ok {
		t.Fatal("framework code 3012 must not resolve")
	}

	if _, ok := dvpn.ErrorFromRPCError(fmt.Errorf("plain")); ok {
		t.Fatal("plain error must not resolve")
	}
}

func TestParseProgramError(t *testing.T) {
	ev := dvpn.NodeOpened{Bond: 5}

	body, err := ev.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	line := "Program data: " + base64.StdEncoding.EncodeToString(append(dvpn.NodeOpenedEventDiscriminator[:], body...))

	events := dvpn.ParseEvents([]string{"Program log: x", line})
	if len(events) != 1 || events[0].(*dvpn.NodeOpened).Bond != 5 {
		t.Fatalf("events = %v", events)
	}

	err = dvpn.ParseProgramError([]string{"Program log: AnchorError occurred. Error Code: Unauthorized. Error Number: 6000. Error Message: nope."})
	if !errors.Is(err, dvpn.ErrUnauthorized) {
		t.Fatalf("ParseProgramError = %v", err)
	}

	if !errors.Is(fmt.Errorf("wrapped: %w", &dvpn.Error{Code: 6000}), dvpn.ErrUnauthorized) {
		t.Fatal("Is must compare codes")
	}

	if errors.Is(dvpn.ErrUnauthorized, (*dvpn.Error)(nil)) {
		t.Fatal("Is must reject a typed-nil target")
	}

	if (*dvpn.Error)(nil).Is(dvpn.ErrUnauthorized) {
		t.Fatal("Is must reject a typed-nil receiver")
	}
}

func TestResolveCloseChannelParams(t *testing.T) {
	owner := solana.MustPublicKeyFromBase58("11111111111111111111111111111112")
	channel := solana.MustPublicKeyFromBase58("11111111111111111111111111111116")
	given := solana.MustPublicKeyFromBase58("11111111111111111111111111111117")
	rentRecipient := solana.MustPublicKeyFromBase58("11111111111111111111111111111118")

	body, err := dvpn.Channel{Owner: owner, RentRecipient: rentRecipient}.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	srv := coretest.New(t)
	srv.SetAccount(channel, coretest.Account{
		Owner: dvpn.ProgramID,
		Data:  append(dvpn.ChannelAccountDiscriminator[:], body...),
	})

	p, err := dvpn.ResolveCloseChannelParams(
		context.Background(), srv.Client(), core.Options{}, dvpn.ProgramID, dvpn.CloseChannelParams{Channel: channel},
	)
	if err != nil {
		t.Fatal(err)
	}

	user, _, _ := dvpn.FindUserAddress(dvpn.ProgramID, owner)
	if p.User != user || p.RentRecipient != rentRecipient {
		t.Fatalf("resolved %+v", p)
	}

	p, err = dvpn.ResolveCloseChannelParams(
		context.Background(), srv.Client(), core.Options{}, dvpn.ProgramID,
		dvpn.CloseChannelParams{Channel: channel, User: given, RentRecipient: given},
	)
	if err != nil || p.User != given || p.RentRecipient != given {
		t.Fatalf("given values: %+v %v", p, err)
	}
}

func TestResolveCloseNodeParams(t *testing.T) {
	owner := solana.MustPublicKeyFromBase58("11111111111111111111111111111112")
	node := solana.MustPublicKeyFromBase58("11111111111111111111111111111116")
	given := solana.MustPublicKeyFromBase58("11111111111111111111111111111117")
	rentRecipient := solana.MustPublicKeyFromBase58("11111111111111111111111111111118")

	var id [32]uint8
	id[0] = 9

	body, err := dvpn.Node{Owner: owner, NodeID: id, RentRecipient: rentRecipient}.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	srv := coretest.New(t)
	srv.SetAccount(node, coretest.Account{Owner: dvpn.ProgramID, Data: append(dvpn.NodeAccountDiscriminator[:], body...)})

	p, err := dvpn.ResolveCloseNodeParams(context.Background(), srv.Client(), core.Options{}, dvpn.ProgramID, dvpn.CloseNodeParams{Node: node})
	if err != nil {
		t.Fatal(err)
	}

	unbonding, _, _ := dvpn.FindNodeUnbondingAddress(dvpn.ProgramID, owner, id)
	if p.Owner != owner || p.NodeUnbonding != unbonding {
		t.Fatalf("resolved %+v", p)
	}

	if p.RentRecipient != rentRecipient {
		t.Fatalf("rent recipient = %s, want %s", p.RentRecipient, rentRecipient)
	}

	p, err = dvpn.ResolveCloseNodeParams(context.Background(), srv.Client(), core.Options{}, dvpn.ProgramID, dvpn.CloseNodeParams{Node: node, Owner: given, NodeUnbonding: given, RentRecipient: given})
	if err != nil || p.Owner != given || p.NodeUnbonding != given || p.RentRecipient != given {
		t.Fatalf("given values: %+v %v", p, err)
	}

	if _, err := dvpn.ResolveCloseNodeParams(context.Background(), srv.Client(), core.Options{}, dvpn.ProgramID, dvpn.CloseNodeParams{}); !errors.Is(err, core.ErrMissingParam) {
		t.Fatalf("missing node: %v", err)
	}
}

func instructionErrorFixture(code string) map[string]any {
	return map[string]any{"InstructionError": []any{json.Number("0"), map[string]any{"Custom": json.Number(code)}}}
}
