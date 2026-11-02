package program_test

import (
	"testing"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestClient_Config(t *testing.T) {
	t.Parallel()

	programClient, fakeRPC := newClient(t)
	treasury := solana.PublicKey{5}

	config, _, err := dvpn.FindConfigAddress(programClient.ProgramID)
	require.NoError(t, err)

	cfg := dvpn.Config{
		Mint:     programClient.Mint,
		Treasury: treasury,
	}
	configAcc := sdktest.Account{
		Data:  sdktest.EncodeAccount(dvpn.ConfigAccountDiscriminator, cfg),
		Owner: programClient.ProgramID,
	}
	fakeRPC.SetAccount(config, configAcc)

	got, err := programClient.Config(t.Context())
	require.NoError(t, err)
	require.Equal(t, treasury, got.Treasury)
	require.Equal(t, programClient.Mint, got.Mint)
}

func TestClient_FindGrant(t *testing.T) {
	t.Parallel()

	programClient, fakeRPC := newClient(t)
	owner := solana.PublicKey{1}
	operator := solana.PublicKey{5}
	node := solana.PublicKey{7}

	got, err := programClient.FindGrant(t.Context(), owner, operator, dvpn.PermListNode, node)
	require.NoError(t, err)
	require.Nil(t, got)

	require.NoError(t, fakeRPC.SetGrant(programClient.ProgramID, owner, operator, node, dvpn.PermListNode))

	got, err = programClient.FindGrant(t.Context(), owner, operator, dvpn.PermListNode, node)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, node, got.Value.Target)

	got, err = programClient.FindGrant(t.Context(), owner, operator, dvpn.PermListNode, solana.PublicKey{8})
	require.NoError(t, err)
	require.Nil(t, got)

	got, err = programClient.FindGrant(t.Context(), owner, operator, dvpn.PermUpdateNode, node)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestClient_FindGrant_ExpiredOrForeignAccountAbsent(t *testing.T) {
	t.Parallel()

	programClient, fakeRPC := newClient(t)
	owner := solana.PublicKey{1}
	operator := solana.PublicKey{5}

	address, _, err := dvpn.FindGrantAddress(programClient.ProgramID, owner, operator, solana.PublicKey{})
	require.NoError(t, err)

	expired := dvpn.Grant{
		ExpiresAt:   time.Now().Add(-time.Hour).Unix(),
		Grantee:     operator,
		Granter:     owner,
		Permissions: dvpn.PermListNode,
	}
	fakeRPC.SetAccount(address, sdktest.Account{
		Data:  sdktest.EncodeAccount(dvpn.GrantAccountDiscriminator, expired),
		Owner: programClient.ProgramID,
	})

	got, err := programClient.FindGrant(t.Context(), owner, operator, dvpn.PermListNode)
	require.NoError(t, err)
	require.Nil(t, got)

	foreign := expired
	foreign.ExpiresAt = 0
	fakeRPC.SetAccount(address, sdktest.Account{
		Data:  sdktest.EncodeAccount(dvpn.GrantAccountDiscriminator, foreign),
		Owner: owner,
	})

	got, err = programClient.FindGrant(t.Context(), owner, operator, dvpn.PermListNode)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestClient_GrantAddress_OwnerNeedsNone(t *testing.T) {
	t.Parallel()

	programClient, fakeRPC := newClient(t)
	owner := solana.PublicKey{1}
	operator := solana.PublicKey{5}

	address, err := programClient.GrantAddress(t.Context(), owner, owner, dvpn.PermListNode)
	require.NoError(t, err)
	require.Nil(t, address)

	require.NoError(t, fakeRPC.SetGrant(programClient.ProgramID, owner, operator, solana.PublicKey{}, program.PermsNode))

	address, err = programClient.GrantAddress(t.Context(), owner, operator, program.PermsNode)
	require.NoError(t, err)

	want, _, err := dvpn.FindGrantAddress(programClient.ProgramID, owner, operator, solana.PublicKey{})
	require.NoError(t, err)
	require.Equal(t, &want, address)
}

func TestClient_RemainingAccounts_WritableNonSigners(t *testing.T) {
	t.Parallel()

	subjects := program.SortedUniqueSubjects([]solana.PublicKey{{1}, {2}, {3}})
	metas := (&program.Client{}).RemainingAccounts(subjects...)
	require.Len(t, metas, len(subjects))

	for i, meta := range metas {
		require.Equal(t, subjects[i], meta.PublicKey)
		require.True(t, meta.IsWritable)
		require.False(t, meta.IsSigner)
	}
}
