//go:build integration

package paymaster_test

import (
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/paymaster"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const walletBaseUnits uint64 = 1_000_000_000

func TestSender_Send_FallsBackWhenKoraUnreachable(t *testing.T) {
	t.Parallel()

	localnet := sdktest.StartLocalnet(t)
	ctx := t.Context()
	wallet := solana.NewWallet()
	require.NoError(t, localnet.FundSOL(ctx, wallet.PublicKey(), 1))

	unreachable := httptest.NewServer(nil)
	unreachable.Close()

	strict := newSender(t, localnet, tx.PaymasterConfig{URL: unreachable.URL})
	fallback := newSender(t, localnet, tx.PaymasterConfig{Fallback: true, URL: unreachable.URL})

	rentPayer, err := fallback.RentPayer(ctx, wallet.PublicKey())
	require.NoError(t, err)
	require.Equal(t, wallet.PublicKey(), rentPayer)

	ixs := newOpenGrantIxs(t, localnet, wallet.PublicKey(), solana.NewWallet().PublicKey(), rentPayer)
	before := readLamports(t, localnet, wallet.PublicKey())

	_, err = strict.Send(ctx, wallet.PrivateKey, ixs)
	require.ErrorAs(t, err, new(*tx.UnsentError))
	require.Equal(t, before, readLamports(t, localnet, wallet.PublicKey()))

	receipt, err := fallback.Send(ctx, wallet.PrivateKey, ixs)
	require.NoError(t, err)
	require.Equal(t, wallet.PublicKey(), receipt.FeePayer)
	require.Nil(t, receipt.TokenFee)
}

func TestSender_Send_OpensAndClosesGrantWithoutSOL(t *testing.T) {
	t.Parallel()

	localnet := sdktest.StartLocalnet(t)
	kora := sdktest.StartKora(t, localnet)
	ctx := t.Context()
	wallet, grantee := solana.NewWallet(), solana.NewWallet().PublicKey()
	require.NoError(t, localnet.FundTokens(ctx, wallet.PublicKey(), walletBaseUnits))

	sender := newSender(t, localnet, tx.PaymasterConfig{APIKey: kora.APIKey, URL: kora.URL})

	rentPayer, err := sender.RentPayer(ctx, wallet.PublicKey())
	require.NoError(t, err)
	require.Equal(t, kora.Signer, rentPayer)

	openIxs := newOpenGrantIxs(t, localnet, wallet.PublicKey(), grantee, rentPayer)

	opened, err := sender.Send(ctx, wallet.PrivateKey, openIxs)
	require.NoError(t, err)
	require.Equal(t, kora.Signer, opened.FeePayer)
	require.NotNil(t, opened.TokenFee)
	require.Equal(t, localnet.Mint, opened.TokenFee.FeeToken)
	require.Positive(t, opened.TokenFee.BaseUnits)
	require.Zero(t, readLamports(t, localnet, wallet.PublicKey()))
	require.Equal(t, walletBaseUnits-opened.TokenFee.BaseUnits, readTokenBaseUnits(t, localnet, wallet.PublicKey()))

	grant, _, err := dvpn.FindGrantAddress(localnet.ProgramID, wallet.PublicKey(), grantee, solana.PublicKey{})
	require.NoError(t, err)

	rent := readLamports(t, localnet, grant)
	closeParams := dvpn.CloseGrantParams{
		Grant:         grant,
		RentRecipient: wallet.PublicKey(),
		Signer:        wallet.PublicKey(),
	}

	closeIx, err := dvpn.BuildCloseGrantInstruction(localnet.ProgramID, closeParams)
	require.NoError(t, err)

	closed, err := sender.Send(ctx, wallet.PrivateKey, []solana.Instruction{closeIx})
	require.NoError(t, err)
	require.Equal(t, kora.Signer, closed.FeePayer)
	require.Equal(t, rent, readLamports(t, localnet, wallet.PublicKey()))

	require.NoError(t, localnet.FundSOL(ctx, wallet.PublicKey(), 1))

	selfPaidIxs := newOpenGrantIxs(t, localnet, wallet.PublicKey(), grantee, wallet.PublicKey())

	selfPaid, err := tx.Send(ctx, localnet.RPC, localnet.WS, wallet.PrivateKey, tx.DefaultConfig(), selfPaidIxs)
	require.NoError(t, err)

	paymasterUnits := readComputeUnits(t, localnet, opened.Signature)
	selfPaidUnits := readComputeUnits(t, localnet, selfPaid.Signature)
	require.Greater(t, paymasterUnits, selfPaidUnits)
	require.LessOrEqual(t, paymasterUnits-selfPaidUnits, uint64(paymaster.FeeTransferComputeUnits))
}

func newOpenGrantIxs(
	t *testing.T,
	localnet *sdktest.Localnet,
	granter, grantee, rentPayer solana.PublicKey,
) []solana.Instruction {
	t.Helper()

	params := dvpn.OpenGrantParams{
		Grantee:            grantee,
		Granter:            granter,
		GrantRentRecipient: granter,
		Permissions:        dvpn.PermOpenChannel,
		RentPayer:          rentPayer,
	}

	ix, err := dvpn.BuildOpenGrantInstruction(localnet.ProgramID, params)
	require.NoError(t, err)

	return []solana.Instruction{ix}
}

func newSender(t *testing.T, localnet *sdktest.Localnet, cfg tx.PaymasterConfig) *tx.Sender {
	t.Helper()

	koraPaymaster, err := paymaster.NewKora(localnet.RPC, cfg, localnet.Mint)
	require.NoError(t, err)

	sender := tx.NewSender(localnet.RPC, nil, tx.DefaultConfig(), zerolog.New(zerolog.NewTestWriter(t)))
	sender.Paymaster = koraPaymaster

	return sender
}

func readComputeUnits(t *testing.T, localnet *sdktest.Localnet, sig solana.Signature) uint64 {
	t.Helper()

	version := rpc.MaxSupportedTransactionVersion1
	opts := rpc.GetTransactionOpts{
		Commitment:                     rpc.CommitmentConfirmed,
		Encoding:                       solana.EncodingBase64,
		MaxSupportedTransactionVersion: &version,
	}

	resp, err := localnet.RPC.GetTransaction(t.Context(), sig, &opts)
	require.NoError(t, err)
	require.NotNil(t, resp.Meta.ComputeUnitsConsumed)

	return *resp.Meta.ComputeUnitsConsumed
}

func readLamports(t *testing.T, localnet *sdktest.Localnet, address solana.PublicKey) uint64 {
	t.Helper()

	resp, err := localnet.RPC.GetBalance(t.Context(), address, rpc.CommitmentConfirmed)
	require.NoError(t, err)

	return resp.Value
}

func readTokenBaseUnits(t *testing.T, localnet *sdktest.Localnet, owner solana.PublicKey) uint64 {
	t.Helper()

	account, _, err := solana.FindAssociatedTokenAddressWithProgram(owner, localnet.Mint, solana.Token2022ProgramID)
	require.NoError(t, err)

	resp, err := localnet.RPC.GetTokenAccountBalance(t.Context(), account, rpc.CommitmentConfirmed)
	require.NoError(t, err)

	baseUnits, err := strconv.ParseUint(resp.Value.Amount, 10, 64)
	require.NoError(t, err)

	return baseUnits
}
