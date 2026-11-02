//go:build integration

package tx_test

import (
	"testing"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
)

func TestSend_ConfirmsOverWebsocket(t *testing.T) {
	t.Parallel()

	localnet := sdktest.StartLocalnet(t)
	ctx := t.Context()

	recipient, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	receipt, err := tx.Send(
		ctx,
		localnet.RPC,
		localnet.WS,
		localnet.Authority,
		tx.DefaultConfig(),
		transferIxs(localnet.Authority.PublicKey(), recipient.PublicKey()),
	)
	require.NoError(t, err)
	require.NotZero(t, receipt.Slot)
	require.NotEqual(t, solana.Signature{}, receipt.Signature)
}

func TestSend_WithFixedPriorityFee(t *testing.T) {
	t.Parallel()

	localnet := sdktest.StartLocalnet(t)
	ctx := t.Context()

	recipient, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	cfg := tx.DefaultConfig()
	cfg.PriorityFeeMode = tx.PriorityFeeModeFixed
	cfg.PriorityFeeLamports = 1_000

	receipt, err := tx.Send(
		ctx,
		localnet.RPC,
		localnet.WS,
		localnet.Authority,
		cfg,
		transferIxs(localnet.Authority.PublicKey(), recipient.PublicKey()),
	)
	require.NoError(t, err)
	require.NotZero(t, receipt.Slot)
}
