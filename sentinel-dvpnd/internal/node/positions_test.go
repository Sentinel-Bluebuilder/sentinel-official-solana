//go:build linux

package node_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/sentinel-official/sentinel-dvpnd/internal/node"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/voucher"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestRuntime_SyncPositions_ClosesOnClosing(t *testing.T) {
	t.Parallel()

	r, home, state := newRuntime(t)
	channel := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeStream)
	req := nodeapi.OpenSessionRequest{
		Credential: nodeapi.Credential{Channel: &channel, Type: nodeapi.CredentialTypeChannel},
	}
	admitted, err := node.Admit(t.Context(), r, home.OwnerAddress, req)
	require.NoError(t, err)
	sess, _, err := node.OpenSession(t.Context(), r, admitted, peerRequestFixture())
	require.NoError(t, err)

	address, closing := channelFixture(
		t, r, home, home.OwnerKey, home.OwnerAddress, home.OwnerAddress, dvpn.PositionModeStream,
	)
	closing.CloseStartedAt = time.Now().Unix()
	closing.Status = dvpn.PositionStatusClosing
	home.RPC.SetAccount(address, sdktest.Account{
		Data:  sdktest.EncodeAccount(dvpn.ChannelAccountDiscriminator, closing),
		Owner: home.ProgramClient.ProgramID,
	})

	require.NoError(t, node.SyncPositions(t.Context(), r))

	var got node.Session
	require.NoError(t, home.DB.WithContext(t.Context()).First(&got, "id = ?", sess.ID).Error)
	require.Equal(t, node.SessionStatusClosed, got.Status)
	require.Equal(t, []service.PeerID{service.PeerID(sess.PeerID)}, state.RemovedPeers())
}

func TestRuntime_SyncPositions_ClosesWithoutLosingVoucher(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	address := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
	seedPosition(t, r, home, address, 100)
	_, err := node.NewHandler(r).GetChannel(
		newSignedContext(t, home.OwnerAddress), nodeapi.GetChannelRequestObject{Channel: address},
	)
	require.NoError(t, err)

	signed := voucher.Sign(home.OwnerKey, voucher.ChannelMessage(address, channelOpenedUnix, 200))

	var advanced atomic.Bool

	require.NoError(t, home.DB.Callback().Query().After("gorm:query").Register("test:advance", func(db *gorm.DB) {
		if _, ok := db.Statement.Dest.(*[]node.Position); !ok || advanced.Swap(true) {
			return
		}

		_, acceptErr := node.AcceptChannelVoucher(t.Context(), r, address, home.OwnerAddress, nodeapi.Voucher{
			Cumulative: 200,
			Signature:  signed.Signature[:],
		})
		require.NoError(t, acceptErr)
		home.RPC.SetAccount(address, sdktest.Account{Owner: solana.PublicKey{42}})
	}))
	require.NoError(t, node.SyncPositions(t.Context(), r))

	var stored node.Position
	require.NoError(t, home.DB.WithContext(t.Context()).First(&stored, "address = ?", address.String()).Error)
	require.Equal(t, node.PositionStatusClosed, stored.Status)
	require.EqualValues(t, 200, stored.Cumulative)
	require.Equal(t, signed.Signature[:], stored.Signature)
	require.Greater(t, stored.AcceptedUnix, channelOpenedUnix)
}
