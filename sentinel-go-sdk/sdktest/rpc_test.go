package sdktest_test

import (
	"testing"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestFakeRPC_ServeHTTP_Accounts(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	owner := solana.PublicKey{1}
	address := solana.PublicKey{2}

	account := sdktest.Account{
		Data:     []byte{1, 2, 3, 4, 5, 6, 7, 8, 9},
		Lamports: 10,
		Owner:    owner,
	}
	fakeRPC.SetAccount(address, account)

	rpcClient := fakeRPC.NewClient()

	resp, err := rpcClient.GetAccountInfo(t.Context(), address)
	require.NoError(t, err)
	require.Equal(t, owner, resp.Value.Owner)
	require.Equal(t, account.Data, resp.Value.Data.GetBinary())

	_, err = rpcClient.GetAccountInfo(t.Context(), owner)
	require.ErrorIs(t, err, rpc.ErrNotFound)

	memcmp := rpc.RPCFilterMemcmp{
		Offset: 1,
		Bytes:  solana.Base58([]byte{2, 3}),
	}
	filters := []rpc.RPCFilter{{Memcmp: &memcmp}}
	opts := rpc.GetProgramAccountsOpts{
		Encoding: solana.EncodingBase64,
		Filters:  filters,
	}

	out, err := rpcClient.GetProgramAccountsWithContext(t.Context(), owner, &opts)
	require.NoError(t, err)
	require.Equal(t, uint64(100), out.Context.Slot)
	require.Len(t, out.Value, 1)
	require.Equal(t, address, out.Value[0].Pubkey)

	memcmp.Bytes = solana.Base58([]byte{9, 9})

	out, err = rpcClient.GetProgramAccountsWithContext(t.Context(), owner, &opts)
	require.NoError(t, err)
	require.Empty(t, out.Value)
}

func TestFakeRPC_ServeHTTP_DataSizeFilter(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	programID := dvpn.ProgramID
	address := solana.PublicKey{1}
	lease := dvpn.Lease{Provider: solana.PublicKey{2}}
	fakeRPC.SetAccount(address, sdktest.Account{
		Data:  sdktest.EncodeAccount(dvpn.LeaseAccountDiscriminator, lease),
		Owner: programID,
	})

	opts := core.Options{Commitment: rpc.CommitmentConfirmed, Owner: programID}

	got, err := dvpn.FindLeaseAccounts(t.Context(), fakeRPC.NewClient(), programID, opts)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, address, got[0].Address)

	none, err := dvpn.FindLeaseAccounts(t.Context(), fakeRPC.NewClient(), programID, opts, rpc.RPCFilter{DataSize: 1})
	require.NoError(t, err)
	require.Empty(t, none)
}
