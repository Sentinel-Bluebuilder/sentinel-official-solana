//go:build linux

package node_test

import (
	"testing"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/node"
	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

// seedClosedSession records a session on address that delivered rxBytes at price per GB.
func seedClosedSession(
	t *testing.T, home testutil.Home, address solana.PublicKey, id string, created int64, rxBytes, price uint64,
) uint64 {
	t.Helper()

	sess := &node.Session{
		Basis:       uint8(session.BasisPerGB),
		CreatedUnix: created,
		ID:          id,
		PeerID:      "peer-" + id,
		Price:       price,
		RxBytes:     rxBytes,
		Status:      node.SessionStatusClosed,
		Subject:     address.String(),
	}
	require.NoError(t, home.DB.WithContext(t.Context()).Create(sess).Error)

	return session.Owed(session.BasisPerGB, price, rxBytes)
}

// The session ran past its credit before the cut and settled only up to the credit it had then.
// A later voucher covers the rest, so the claim follows what was delivered.
func TestRuntime_Redeem_ClaimCoversUsagePastTheCreditAtClose(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	address := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
	threshold := home.Loaded.Node.Redeem.ThresholdBaseUnits
	cumulative := threshold * 10
	seedPosition(t, r, home, address, cumulative)

	const price = 1_000_000_000

	delivered := seedClosedSession(t, home, address, "past-credit", time.Now().Unix(), threshold*6, price)
	require.NoError(t, home.DB.Model(&node.Position{}).Where("address = ?", address.String()).
		Update("settled", threshold*4).Error)

	require.NoError(t, node.Redeem(t.Context(), r))

	sent := home.RPC.Sent()
	require.Len(t, sent, 1)
	require.Equal(t, []uint64{delivered}, partialClaims(t, sent[0].Message.Instructions[0].Data))
}

// Sessions from before the position opened belong to an earlier channel at the same address.
func TestRuntime_Redeem_ClaimIgnoresSessionsFromBeforeThePositionOpened(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	address := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
	threshold := home.Loaded.Node.Redeem.ThresholdBaseUnits
	cumulative := threshold * 10
	seedPosition(t, r, home, address, cumulative)

	var opened int64
	require.NoError(t, home.DB.Model(&node.Position{}).Select("opened_unix").
		Where("address = ?", address.String()).Scan(&opened).Error)

	const price = 1_000_000_000

	seedClosedSession(t, home, address, "earlier-channel", opened-3600, threshold*8, price)
	current := seedClosedSession(t, home, address, "this-channel", opened, threshold*3, price)
	require.NoError(t, home.DB.Model(&node.Position{}).Where("address = ?", address.String()).
		Update("settled", threshold*2).Error)

	require.NoError(t, node.Redeem(t.Context(), r))

	sent := home.RPC.Sent()
	require.Len(t, sent, 1)
	require.Equal(t, []uint64{current}, partialClaims(t, sent[0].Message.Instructions[0].Data))
}
