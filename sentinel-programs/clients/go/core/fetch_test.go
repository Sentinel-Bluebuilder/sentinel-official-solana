package core_test

import (
	"errors"
	"testing"

	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/sentinel-official/sentinel-programs/clients/go/core/coretest"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
)

var (
	address = solana.MustPublicKeyFromBase58("11111111111111111111111111111113")
	other   = solana.MustPublicKeyFromBase58("11111111111111111111111111111114")
	owner   = solana.MustPublicKeyFromBase58("11111111111111111111111111111112")
)

func TestFetch(t *testing.T) {
	srv := coretest.New(t)
	srv.SetAccount(address, coretest.Account{Owner: owner, Data: []byte{7}})
	srv.SetSlot(42)

	got, err := core.Fetch(t.Context(), srv.Client(), address, core.Options{Owner: owner}, fakeDecodeFirstByte)
	if err != nil {
		t.Fatal(err)
	}

	if got.Address != address || got.Slot != 42 || got.Value != 7 {
		t.Fatalf("got %+v", got)
	}

	if _, err := core.Fetch(t.Context(), srv.Client(), address, core.Options{Owner: other}, fakeDecodeFirstByte); !errors.Is(err, core.ErrOwnerMismatch) {
		t.Fatalf("wrong owner: %v", err)
	}

	if _, err := core.Fetch(t.Context(), srv.Client(), other, core.Options{}, fakeDecodeFirstByte); !errors.Is(err, core.ErrAccountNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestFetchBatch(t *testing.T) {
	srv := coretest.New(t)
	srv.SetAccount(address, coretest.Account{Owner: owner, Data: []byte{1}})
	srv.SetAccount(other, coretest.Account{Owner: owner, Data: []byte{2}})

	got, err := core.FetchBatch(t.Context(), srv.Client(), []solana.PublicKey{other, address}, core.Options{Owner: owner}, fakeDecodeFirstByte)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 || got[0].Address != other || got[0].Value != 2 || got[1].Address != address || got[1].Value != 1 {
		t.Fatalf("got %+v", got)
	}

	if _, err := core.FetchBatch(t.Context(), srv.Client(), []solana.PublicKey{address, owner}, core.Options{}, fakeDecodeFirstByte); !errors.Is(err, core.ErrAccountNotFound) {
		t.Fatalf("missing: %v", err)
	}

	if got, err := core.FetchBatch(t.Context(), nil, nil, core.Options{}, fakeDecodeFirstByte); err != nil || got != nil {
		t.Fatalf("empty: %v %v", got, err)
	}
}

func TestFindAccounts(t *testing.T) {
	srv := coretest.New(t)
	srv.SetSlot(42)
	srv.SetAccount(address, coretest.Account{Owner: owner, Data: []byte{1, 5}})
	srv.SetAccount(other, coretest.Account{Owner: owner, Data: []byte{2, 5}})
	srv.SetAccount(owner, coretest.Account{Owner: owner, Data: []byte{1, 5, 0}})

	filters := []rpc.RPCFilter{{DataSize: 2}, {Memcmp: &rpc.RPCFilterMemcmp{Offset: 1, Bytes: solana.Base58{5}}}}

	got, err := core.FindAccounts(t.Context(), srv.Client(), owner, core.Options{}, filters, fakeDecodeFirstByte)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 || got[0].Address != address || got[1].Address != other || got[0].Slot != 42 {
		t.Fatalf("got %+v", got)
	}

	got, err = core.FindAccounts(t.Context(), srv.Client(), owner, core.Options{}, []rpc.RPCFilter{{Memcmp: &rpc.RPCFilterMemcmp{Offset: 0, Bytes: solana.Base58{2}}}}, fakeDecodeFirstByte)
	if err != nil || len(got) != 1 || got[0].Value != 2 {
		t.Fatalf("memcmp: %v %v", got, err)
	}

	srv.SetAccount(address, coretest.Account{
		Owner: owner,
		Data:  []byte{1, 2, 3, 4, 5, 6},
	})
	srv.SetAccount(other, coretest.Account{
		Owner: owner,
		Data:  []byte{1, 2, 3, 4, 9, 6},
	})
	srv.SetAccount(owner, coretest.Account{
		Owner: owner,
		Data:  []byte{1, 2, 3, 4, 5, 6, 7},
	})

	filters = []rpc.RPCFilter{
		{Memcmp: &rpc.RPCFilterMemcmp{Offset: 0, Bytes: solana.Base58{1}}},
		{DataSize: 6},
		{Memcmp: &rpc.RPCFilterMemcmp{Offset: 1, Bytes: solana.Base58{2}}},
		{Memcmp: &rpc.RPCFilterMemcmp{Offset: 2, Bytes: solana.Base58{3}}},
		{Memcmp: &rpc.RPCFilterMemcmp{Offset: 3, Bytes: solana.Base58{4}}},
		{Memcmp: &rpc.RPCFilterMemcmp{Offset: 4, Bytes: solana.Base58{5}}},
	}
	got, err = core.FindAccounts(t.Context(), srv.Client(), owner, core.Options{}, filters, fakeDecodeFirstByte)
	if err != nil || len(got) != 1 || got[0].Address != address {
		t.Fatalf("overflow filters: %v %v", got, err)
	}
}

func fakeDecodeFirstByte(b []byte) (*byte, error) {
	if len(b) == 0 {
		return nil, errors.New("empty")
	}

	return &b[0], nil
}
