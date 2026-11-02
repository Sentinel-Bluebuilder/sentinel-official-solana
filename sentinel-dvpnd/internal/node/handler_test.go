//go:build linux

package node_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/solana-foundation/solana-go/v2/rpc/jsonrpc"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/sentinel-official/sentinel-dvpnd/internal/node"
	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
	"github.com/sentinel-official/sentinel-go-sdk/v3/version"
	"github.com/sentinel-official/sentinel-go-sdk/v3/voucher"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

type failingRemoveOnceServer struct {
	service.Server

	failed atomic.Bool
}

func (s *failingRemoveOnceServer) RemovePeer(ctx context.Context, id service.PeerID) error {
	if !s.failed.Swap(true) {
		return errors.New("peer removal failed")
	}

	if err := s.Server.RemovePeer(ctx, id); err != nil {
		return fmt.Errorf("removing peer: %w", err)
	}

	return nil
}

type fakeSlotRPCClient struct {
	rpc.JSONRPCClient

	slot atomic.Uint64
}

func (c *fakeSlotRPCClient) CallForInto(ctx context.Context, out any, method string, params []any) error {
	slot := c.slot.Load()
	if err := c.JSONRPCClient.CallForInto(ctx, out, method, params); err != nil {
		return fmt.Errorf("calling RPC: %w", err)
	}

	if account, ok := out.(**rpc.GetAccountInfoResult); ok && *account != nil {
		(*account).Context.Slot = slot
	}

	return nil
}

func TestHandler_GetChannel_DropsSupersededSignature(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	address, channel := channelFixture(
		t, r, home, home.OwnerKey, home.OwnerAddress, home.OwnerAddress, dvpn.PositionModeVoucher,
	)
	seedPosition(t, r, home, address, 100)

	channel.Redeemed = 200
	home.RPC.SetAccount(address, sdktest.Account{
		Data:  sdktest.EncodeAccount(dvpn.ChannelAccountDiscriminator, channel),
		Owner: home.ProgramClient.ProgramID,
	})
	response, err := node.NewHandler(r).GetChannel(
		newSignedContext(t, home.OwnerAddress), nodeapi.GetChannelRequestObject{Channel: address},
	)
	require.NoError(t, err)

	view, ok := response.(nodeapi.GetChannel200JSONResponse)
	require.True(t, ok)
	require.EqualValues(t, 200, view.Ledger.CumulativeBaseUnits)
	require.Empty(t, view.Ledger.Signature)

	var stored node.Position
	require.NoError(t, home.DB.WithContext(t.Context()).First(&stored, "address = ?", address.String()).Error)
	require.Zero(t, stored.AcceptedUnix)
	require.Empty(t, stored.Signature)
}

func TestHandler_GetChannel_OffersCurrentPriceAfterClose(t *testing.T) {
	t.Parallel()

	r, home, state := newRuntime(t)
	_, chainNode := node.ChainState(r)
	chainNode.ChannelPricePerGB = 1000
	node.SetChainNode(r, chainNode)
	address := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
	terms := session.Terms{Basis: session.BasisPerGB, Price: 1000}
	admitted, err := node.Admit(t.Context(), r, home.OwnerAddress, voucherRequest(t, home, address, terms, 100))
	require.NoError(t, err)
	sess, _, err := node.OpenSession(t.Context(), r, admitted, peerRequestFixture())
	require.NoError(t, err)
	state.SetStats(service.PeerID(sess.PeerID), service.PeerStats{RxBytes: 25_000_000})
	require.NoError(t, node.SyncUsage(t.Context(), r))

	chainNode.ChannelPricePerGB = 2000
	node.SetChainNode(r, chainNode)

	ctx := newSignedContext(t, home.OwnerAddress)
	response, err := node.NewHandler(r).GetChannel(ctx, nodeapi.GetChannelRequestObject{Channel: address})
	require.NoError(t, err)

	live, ok := response.(nodeapi.GetChannel200JSONResponse)
	require.True(t, ok)
	require.EqualValues(t, 1000, live.Ledger.Price)
	require.EqualValues(t, 25, live.Consumption.OwedBaseUnits)
	require.NoError(t, node.CloseSession(t.Context(), r, sess, "closed by client"))
	response, err = node.NewHandler(r).GetChannel(ctx, nodeapi.GetChannelRequestObject{Channel: address})
	require.NoError(t, err)

	offer, ok := response.(nodeapi.GetChannel200JSONResponse)
	require.True(t, ok)
	require.EqualValues(t, 2000, offer.Ledger.Price)
	require.EqualValues(t, 25, offer.Consumption.SettledBaseUnits)

	historical, err := node.NewHandler(r).GetSession(ctx, nodeapi.GetSessionRequestObject{ID: sess.ID})
	require.NoError(t, err)

	closed, ok := historical.(nodeapi.GetSession200JSONResponse)
	require.True(t, ok)
	require.EqualValues(t, 1000, closed.Ledger.Price)
	terms.Price = 2000
	req := voucherRequest(t, home, address, terms, 200)
	req.Peer = peerRequestFixture()
	opened, err := node.NewHandler(r).OpenSession(ctx, nodeapi.OpenSessionRequestObject{Body: &req})
	require.NoError(t, err)

	result, ok := opened.(nodeapi.OpenSession200JSONResponse)
	require.True(t, ok)
	require.EqualValues(t, 2000, result.Ledger.Price)
	require.NotEqual(t, sess.ID, result.SessionID)
}

func TestHandler_GetChannel_PreservesConcurrentVoucherAndRedemption(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	address := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
	seedPosition(t, r, home, address, 100)
	home.Loaded.Node.Redeem.ThresholdBaseUnits = 1
	signed := voucher.Sign(home.OwnerKey, voucher.ChannelMessage(address, channelOpenedUnix, 200))

	var (
		advanced atomic.Bool
		accepted *node.Position
	)

	require.NoError(t, home.DB.Callback().Query().After("gorm:query").Register("test:advance", func(db *gorm.DB) {
		if _, ok := db.Statement.Dest.(*node.Position); !ok || advanced.Swap(true) {
			return
		}

		var err error

		accepted, err = node.AcceptChannelVoucher(t.Context(), r, address, home.OwnerAddress, nodeapi.Voucher{
			Cumulative: 200,
			Signature:  signed.Signature[:],
		})
		require.NoError(t, err)
		require.NoError(t, node.Redeem(t.Context(), r))
	}))
	response, err := node.NewHandler(r).GetChannel(
		newSignedContext(t, home.OwnerAddress), nodeapi.GetChannelRequestObject{Channel: address},
	)
	require.NoError(t, err)

	view, ok := response.(nodeapi.GetChannel200JSONResponse)
	require.True(t, ok)
	require.EqualValues(t, 200, view.Ledger.CumulativeBaseUnits)
	require.EqualValues(t, 200, view.Ledger.RedeemedBaseUnits)
	require.Equal(t, signed.Signature[:], view.Ledger.Signature)

	var stored node.Position
	require.NoError(t, home.DB.WithContext(t.Context()).First(&stored, "address = ?", address.String()).Error)
	require.Equal(t, accepted.AcceptedUnix, stored.AcceptedUnix)
	require.Equal(t, signed.Signature[:], stored.Signature)
	require.EqualValues(t, 200, stored.Cumulative)
	require.EqualValues(t, 200, stored.Redeemed)
	require.NotZero(t, stored.RedeemedUnix)
	require.GreaterOrEqual(t, stored.UpdatedUnix, accepted.UpdatedUnix)
}

func TestHandler_GetChannel_RefreshesFromChain(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	address := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
	client := newHandlerClient(t, startHandlerServer(t, r), home.OwnerKey)
	response, err := client.GetChannelWithResponse(t.Context(), address)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode())
	require.NotNil(t, response.JSON200)
	require.EqualValues(t, channelEscrow, response.JSON200.Ledger.EscrowBaseUnits)
	require.Equal(t, nodeapi.LedgerStatusOpen, response.JSON200.Ledger.Status)

	var position node.Position
	require.NoError(t, home.DB.WithContext(t.Context()).First(&position, "address = ?", address.String()).Error)
	require.EqualValues(t, channelEscrow, position.Escrow)
}

func TestHandler_GetChannel_RejectsDelayedChainSnapshot(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{"closing", "reopened"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()

			r, home, _ := newRuntime(t)
			client := &fakeSlotRPCClient{JSONRPCClient: jsonrpc.NewClient(home.RPC.Server.URL)}
			client.slot.Store(100)
			home.ProgramClient.RPC = rpc.NewWithCustomRPCClient(client)
			address := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
			seedPosition(t, r, home, address, 100)
			require.NoError(t, home.DB.WithContext(t.Context()).Model(&node.Position{}).
				Where("address = ?", address.String()).Update("settled", 11).Error)

			var advanced atomic.Bool

			require.NoError(t, home.DB.Callback().Query().After("gorm:query").Register("test:advance", func(db *gorm.DB) {
				if _, ok := db.Statement.Dest.(*node.Position); !ok || advanced.Swap(true) {
					return
				}

				_, channel := channelFixture(
					t, r, home, home.OwnerKey, home.OwnerAddress, home.OwnerAddress, dvpn.PositionModeVoucher,
				)
				if scenario == "closing" {
					channel.Status = dvpn.PositionStatusClosing
					channel.CloseStartedAt = time.Now().Unix()
				} else {
					channel.OpenedAt++
				}

				client.slot.Store(101)
				home.RPC.SetAccount(address, sdktest.Account{
					Data:  sdktest.EncodeAccount(dvpn.ChannelAccountDiscriminator, channel),
					Owner: home.ProgramClient.ProgramID,
				})
				_, err := node.NewHandler(r).GetChannel(
					newSignedContext(t, home.OwnerAddress), nodeapi.GetChannelRequestObject{Channel: address},
				)
				require.NoError(t, err)
			}))
			response, err := node.NewHandler(r).GetChannel(
				newSignedContext(t, home.OwnerAddress), nodeapi.GetChannelRequestObject{Channel: address},
			)
			require.NoError(t, err)

			view, ok := response.(nodeapi.GetChannel200JSONResponse)
			require.True(t, ok)

			if scenario == "closing" {
				require.Equal(t, nodeapi.LedgerStatusClosing, view.Ledger.Status)
				require.EqualValues(t, 11, view.Consumption.SettledBaseUnits)
			} else {
				require.Equal(t, channelOpenedUnix+1, view.Ledger.OpenedAt)
				require.Zero(t, view.Ledger.CumulativeBaseUnits)
				require.Empty(t, view.Ledger.Signature)
				require.Zero(t, view.Consumption.SettledBaseUnits)
			}
		})
	}
}

func TestHandler_GetLease_PreservesZeroPinAfterRestart(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	address := seedHandlerLease(t, r, home)
	require.NoError(t, home.DB.WithContext(t.Context()).Delete(&node.Position{}, "address = ?", address.String()).Error)
	account, err := home.ProgramClient.FetchLease(t.Context(), address)
	require.NoError(t, err)

	account.Value.RatePerHour = 0
	home.RPC.SetAccount(address, sdktest.Account{
		Data:  sdktest.EncodeAccount(dvpn.LeaseAccountDiscriminator, account.Value),
		Owner: home.ProgramClient.ProgramID,
	})

	_, firstNode := node.ChainState(r)
	firstNode.LeasePricePerHour = 0
	node.SetChainNode(r, firstNode)
	initial, err := node.NewHandler(r).GetLease(
		newSignedContext(t, home.OwnerAddress), nodeapi.GetLeaseRequestObject{Lease: address},
	)
	require.NoError(t, err)

	first, ok := initial.(nodeapi.GetLease200JSONResponse)
	require.True(t, ok)
	require.Zero(t, first.Ledger.Price)

	restarted, err := node.New(home.Loaded, home.DB, home.DaemonKey, home.ProgramClient,
		home.Sender, zerolog.Nop(), prometheus.NewRegistry())
	require.NoError(t, err)
	require.NoError(t, node.SyncChain(t.Context(), restarted))
	_, chainNode := node.ChainState(restarted)
	chainNode.LeasePricePerHour = 20
	node.SetChainNode(restarted, chainNode)
	response, err := node.NewHandler(restarted).GetLease(
		newSignedContext(t, home.OwnerAddress), nodeapi.GetLeaseRequestObject{Lease: address},
	)
	require.NoError(t, err)

	view, ok := response.(nodeapi.GetLease200JSONResponse)
	require.True(t, ok)
	require.Zero(t, view.Ledger.Price)

	var stored node.Position
	require.NoError(t, home.DB.WithContext(t.Context()).First(&stored, "address = ?", address.String()).Error)
	require.Zero(t, stored.RatePerHour)
}

func TestHandler_GetLease_ReturnsPinnedPrice(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	address := seedHandlerLease(t, r, home)
	client := newHandlerClient(t, startHandlerServer(t, r), home.OwnerKey)
	response, err := client.GetLeaseWithResponse(t.Context(), address)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode())
	require.NotNil(t, response.JSON200)
	require.EqualValues(t, 7, response.JSON200.Ledger.Price)
	require.Equal(t, session.BasisPerHour, response.JSON200.Ledger.Basis)
	require.EqualValues(t, 10_000, response.JSON200.Ledger.EscrowBaseUnits)
}

func TestHandler_GetLeaseEvents_AuthorizesParty(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	home.Loaded.Node.Sessions.HeartbeatSeconds = 1
	address := seedHandlerLease(t, r, home)
	server := startHandlerServer(t, r)
	other := newHandlerClient(t, server, home.DaemonKey)
	denied, err := other.GetLeaseEvents(t.Context(), address)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, denied.Body.Close()) })

	require.Equal(t, http.StatusUnauthorized, denied.StatusCode)
	client := newHandlerClient(t, server, home.OwnerKey)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	response, err := client.GetLeaseEvents(ctx, address)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, response.Body.Close()) })

	require.Equal(t, http.StatusOK, response.StatusCode)
	decoder := api.NewEventDecoder(response.Body)
	frame, err := decoder.Next()
	require.NoError(t, err)
	require.True(t, frame.IsComment)
	home.RPC.SetAccount(address, sdktest.Account{Owner: solana.PublicKey{42}})
	require.NoError(t, node.SyncPositions(t.Context(), r))

	frame = readHandlerEvent(t, decoder)
	require.Equal(t, nodeapi.EventClosed, frame.Name)

	_, err = decoder.Next()
	require.ErrorIs(t, err, io.EOF)
}

func TestHandler_GetLeaseEvents_ClosesAfterLedgerRefresh(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	home.Loaded.Node.Sessions.HeartbeatSeconds = 1
	address := seedHandlerLease(t, r, home)
	client := newHandlerClient(t, startHandlerServer(t, r), home.OwnerKey)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	response, err := client.GetLeaseEvents(ctx, address)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, response.Body.Close()) })
	require.Equal(t, http.StatusOK, response.StatusCode)

	decoder := api.NewEventDecoder(response.Body)
	frame, err := decoder.Next()
	require.NoError(t, err)
	require.True(t, frame.IsComment)

	account, err := home.ProgramClient.FetchLease(t.Context(), address)
	require.NoError(t, err)

	account.Value.Status = dvpn.PositionStatusClosing
	account.Value.CloseStartedAt = time.Now().Unix()
	home.RPC.SetAccount(address, sdktest.Account{
		Data:  sdktest.EncodeAccount(dvpn.LeaseAccountDiscriminator, account.Value),
		Owner: home.ProgramClient.ProgramID,
	})
	ledger, err := client.GetLeaseWithResponse(t.Context(), address)
	require.NoError(t, err)
	require.NotNil(t, ledger.JSON200)
	require.Equal(t, nodeapi.LedgerStatusClosing, ledger.JSON200.Ledger.Status)
	require.NoError(t, node.SyncPositions(t.Context(), r))

	frame = readHandlerEvent(t, decoder)
	require.Equal(t, nodeapi.EventClosed, frame.Name)

	var closed nodeapi.ClosedEvent
	require.NoError(t, json.Unmarshal(frame.Data, &closed))
	require.Equal(t, nodeapi.ClosedReasonPositionClosing, closed.Reason)

	_, err = decoder.Next()
	require.ErrorIs(t, err, io.EOF)
}

func TestHandler_GetSession_AllowsConcurrentInstallation(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	channel := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeStream)
	admitted, err := node.Admit(t.Context(), r, home.OwnerAddress, nodeapi.OpenSessionRequest{
		Credential: nodeapi.Credential{
			Channel: &channel,
			Type:    nodeapi.CredentialTypeChannel,
		},
	})
	require.NoError(t, err)

	historical := &node.Session{
		Basis:   uint8(session.BasisPerGB),
		ID:      "history",
		Signer:  home.OwnerAddress.String(),
		Status:  node.SessionStatusClosed,
		Subject: channel.String(),
	}
	require.NoError(t, home.DB.WithContext(t.Context()).Create(historical).Error)

	ctx, cancel := context.WithTimeout(newSignedContext(t, home.OwnerAddress), time.Second)
	defer cancel()

	createReady, readReady := make(chan struct{}), make(chan struct{})
	allowCreate, allowRead := make(chan struct{}), make(chan struct{})
	createErrors, readErrors := make(chan error, 1), make(chan error, 1)

	require.NoError(t, home.DB.Callback().Create().Before("gorm:begin_transaction").
		Register("test:pause_installation", func(db *gorm.DB) {
			if _, ok := db.Statement.Dest.(*node.Session); !ok {
				return
			}

			close(createReady)

			select {
			case <-allowCreate:
			case <-ctx.Done():
				_ = db.AddError(ctx.Err())
			}
		}))
	require.NoError(t, home.DB.Callback().Query().After("gorm:query").Register("test:pause_view", func(db *gorm.DB) {
		if _, ok := db.Statement.ConnPool.(gorm.TxCommitter); !ok {
			return
		}

		if _, ok := db.Statement.Dest.(*node.Session); !ok {
			return
		}

		close(readReady)

		select {
		case <-allowRead:
		case <-ctx.Done():
			_ = db.AddError(ctx.Err())
		}
	}))

	go func() {
		_, _, openErr := node.OpenSession(ctx, r, admitted, peerRequestFixture())
		createErrors <- openErr
	}()

	select {
	case <-createReady:
	case <-ctx.Done():
		t.Fatal("installation did not begin")
	}

	go func() {
		_, readErr := node.NewHandler(r).GetSession(ctx, nodeapi.GetSessionRequestObject{ID: historical.ID})
		readErrors <- readErr
	}()

	select {
	case <-readReady:
	case <-ctx.Done():
		t.Fatal("session view did not acquire a transaction")
	}

	close(allowCreate)
	close(allowRead)
	require.NoError(t, <-createErrors)
	require.NoError(t, <-readErrors)
}

func TestHandler_GetSession_ClosedConsumption(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mode   uint8
		status string
	}{
		{name: "closed voucher with replacement", status: node.SessionStatusClosed},
		{name: "closed stream", mode: dvpn.PositionModeStream, status: node.SessionStatusClosed},
		{name: "closing stream", mode: dvpn.PositionModeStream, status: node.SessionStatusClosing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r, home, _ := newRuntime(t)
			sess := seedSession(t, home, "session-closed", "peer-closed")
			sess.CloseReason = nodeapi.ReasonLeaseNotFunded
			sess.Signer = home.DaemonKey.PublicKey().String()
			sess.Status = tc.status
			sess.Subject = solana.PublicKey{88}.String()
			sess.RxBytes = 10_000_000
			require.NoError(t, home.DB.WithContext(t.Context()).Create(&node.Position{
				Address: sess.Subject, Cumulative: 101, Escrow: 200, Kind: node.KindChannel,
				Mode: tc.mode, Owner: sess.Signer, RatePerHour: 3600, Settled: 7,
				StreamBase: 31, StreamStartedUnix: time.Now().Unix() + 1000,
			}).Error)
			require.NoError(t, home.DB.WithContext(t.Context()).Save(sess).Error)

			if tc.mode == dvpn.PositionModeVoucher {
				replacement := seedSession(t, home, "session-replacement", "peer-replacement")
				replacement.Subject = sess.Subject
				require.NoError(t, home.DB.WithContext(t.Context()).Save(replacement).Error)
			}

			ctx := newSignedContext(t, home.DaemonKey.PublicKey())
			response, err := node.NewHandler(r).GetSession(ctx, nodeapi.GetSessionRequestObject{ID: sess.ID})
			require.NoError(t, err)

			result, ok := response.(nodeapi.GetSession200JSONResponse)
			require.True(t, ok)
			require.Equal(t, nodeapi.SessionStatus(tc.status), result.Status)
			require.Equal(t, nodeapi.ClosedReasonLeaseNotFunded, result.CloseReason)
			require.EqualValues(t, 7, result.Consumption.OwedBaseUnits)
			require.EqualValues(t, 7, result.Consumption.SettledBaseUnits)
			require.EqualValues(t, 101, result.Ledger.CumulativeBaseUnits)
			require.Zero(t, result.Consumption.Usage)

			if tc.mode == dvpn.PositionModeStream {
				home.RPC.SetAccount(solana.PublicKey{88}, sdktest.Account{
					Data: sdktest.EncodeAccount(dvpn.ChannelAccountDiscriminator, dvpn.Channel{
						Escrow:          200,
						Mode:            dvpn.PositionModeStream,
						Node:            home.NodeAddress,
						Owner:           home.DaemonKey.PublicKey(),
						RatePerHour:     3600,
						StreamBase:      31,
						StreamStartedAt: time.Now().Unix() + 1000,
					}),
					Owner: home.ProgramClient.ProgramID,
				})

				response, err := node.NewHandler(r).GetChannel(ctx, nodeapi.GetChannelRequestObject{
					Channel: solana.PublicKey{88},
				})
				require.NoError(t, err)

				result, ok := response.(nodeapi.GetChannel200JSONResponse)
				require.True(t, ok)
				require.EqualValues(t, 31, result.Consumption.OwedBaseUnits)
				require.Zero(t, result.Consumption.Usage)
			}
		})
	}
}

func TestHandler_GetSession_ConcurrentConsumption(t *testing.T) {
	t.Parallel()

	for name, replaced := range map[string]bool{"sampling": false, "replacement": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r, home, _ := newRuntime(t)
			sess := seedSession(t, home, "session-snapshot", "peer-snapshot")
			sess.Basis = uint8(session.BasisPerGB)
			sess.Price = 1000
			sess.RxBytes = 25_000_000
			sess.Signer = home.DaemonKey.PublicKey().String()
			require.NoError(t, home.DB.WithContext(t.Context()).Save(sess).Error)
			require.NoError(t, home.DB.WithContext(t.Context()).Create(&node.Position{
				Address: sess.Subject, Cumulative: 101, Settled: 7,
			}).Error)
			node.SetEnforcer(r, sess.ID, session.NewMeter(session.Terms{Basis: session.BasisPerGB, Price: 1000}, 101, 7))

			var advanced atomic.Bool

			require.NoError(t, home.DB.Callback().Query().After("gorm:query").Register("test:advance", func(db *gorm.DB) {
				if _, ok := db.Statement.Dest.(*node.Session); !ok || advanced.Swap(true) {
					return
				}

				if !replaced {
					require.NoError(t, home.DB.WithContext(t.Context()).Model(&node.Session{}).
						Where("id = ?", sess.ID).Update("rx_bytes", 50_000_000).Error)

					return
				}

				require.NoError(t, home.DB.WithContext(t.Context()).Model(&node.Session{}).
					Where("id = ?", sess.ID).Update("status", node.SessionStatusClosed).Error)
				require.NoError(t, home.DB.WithContext(t.Context()).Model(&node.Position{}).
					Where("address = ?", sess.Subject).Update("settled", 32).Error)
				replacement := seedSession(t, home, "replacement", "peer-replacement")
				replacement.Subject = sess.Subject
				replacement.Basis = uint8(session.BasisPerHour)
				replacement.Price = 3600
				require.NoError(t, home.DB.WithContext(t.Context()).Save(replacement).Error)
				node.SetEnforcer(r, sess.ID, nil)
				node.SetEnforcer(r, replacement.ID, session.NewMeter(
					session.Terms{Basis: session.BasisPerHour, Price: 3600}, 101, 32,
				))
			}))

			response, err := node.NewHandler(r).GetSession(
				newSignedContext(t, home.DaemonKey.PublicKey()), nodeapi.GetSessionRequestObject{ID: sess.ID},
			)
			require.NoError(t, err)

			result, ok := response.(nodeapi.GetSession200JSONResponse)
			require.True(t, ok)
			require.Equal(t, session.BasisPerGB, result.Ledger.Basis)
			require.EqualValues(t, 1000, result.Ledger.Price)

			if replaced {
				require.Equal(t, nodeapi.SessionStatusClosed, result.Status)
				require.EqualValues(t, 32, result.Consumption.OwedBaseUnits)
				require.Zero(t, result.Consumption.Usage)
			} else {
				require.EqualValues(t, 57, result.Consumption.OwedBaseUnits)
				require.EqualValues(t, 50_000_000, result.Consumption.Usage.RxBytes)
			}
		})
	}
}

func TestHandler_GetSession_Consumption(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		credential string
		mode       uint8
		wantOwed   uint64
	}{
		{name: "voucher includes settled and active usage", credential: node.CredentialChannel, wantOwed: 32},
		{
			name: "stream retains accrued consumption", credential: node.CredentialChannel,
			mode: dvpn.PositionModeStream, wantOwed: 31,
		},
		{name: "ticket needs no position", credential: node.CredentialTicket},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r, home, _ := newRuntime(t)
			sess := seedSession(t, home, "session-usage", "peer-usage")
			sess.Basis = uint8(session.BasisPerGB)
			sess.Credential = tc.credential
			sess.Price = 1000
			sess.RxBytes = 25_000_000

			if tc.mode == dvpn.PositionModeStream {
				sess.Basis = uint8(session.BasisPerHour)
				sess.Price = 3600
			}

			sess.Signer = home.DaemonKey.PublicKey().String()
			require.NoError(t, home.DB.WithContext(t.Context()).Save(sess).Error)

			if tc.credential == node.CredentialChannel {
				require.NoError(t, home.DB.WithContext(t.Context()).Create(&node.Position{
					Address: sess.Subject, Cumulative: 101, Escrow: 200, Mode: tc.mode,
					RatePerHour: 3600, Settled: 7, StreamBase: 31, StreamStartedUnix: time.Now().Unix() + 1000,
				}).Error)

				if tc.mode == dvpn.PositionModeVoucher {
					node.SetEnforcer(r, sess.ID, session.NewMeter(session.Terms{Basis: session.BasisPerGB, Price: 1000}, 101, 7))
				}
			}

			before := time.Now().Unix()
			response, err := node.NewHandler(r).GetSession(
				newSignedContext(t, home.DaemonKey.PublicKey()), nodeapi.GetSessionRequestObject{ID: sess.ID},
			)
			after := time.Now().Unix()

			require.NoError(t, err)

			result, ok := response.(nodeapi.GetSession200JSONResponse)
			require.True(t, ok)
			require.Equal(t, tc.wantOwed, result.Consumption.OwedBaseUnits)
			require.Equal(t, sess.RxBytes, result.Consumption.Usage.RxBytes)
			require.Equal(t, sess.TxBytes, result.Consumption.Usage.TxBytes)
			require.GreaterOrEqual(t, result.Consumption.Usage.Seconds, uint64(max(before-sess.CreatedUnix, 0)))
			require.LessOrEqual(t, result.Consumption.Usage.Seconds, uint64(max(after-sess.CreatedUnix, 0)))

			if tc.credential == node.CredentialTicket {
				require.Zero(t, result.Ledger)
			} else {
				require.EqualValues(t, 101, result.Ledger.CumulativeBaseUnits)
				require.EqualValues(t, 7, result.Consumption.SettledBaseUnits)
			}
		})
	}
}

func TestHandler_GetSession_ConsumptionReadFailure(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	sess := seedSession(t, home, "session-consumption", "peer-consumption")
	sess.Signer = home.DaemonKey.PublicKey().String()
	require.NoError(t, home.DB.WithContext(t.Context()).Save(sess).Error)
	require.NoError(t, home.DB.WithContext(t.Context()).Create(&node.Position{Address: sess.Subject}).Error)

	wantErr := errors.New("consumption read failed")

	require.NoError(t, home.DB.Callback().Query().After("gorm:query").
		Register("test:consumption_failure", func(db *gorm.DB) {
			if db.Statement.Table == "sessions" && strings.Contains(db.Statement.SQL.String(), "subject =") {
				db.Error = wantErr
			}
		}))

	ctx := newSignedContext(t, home.DaemonKey.PublicKey())
	response, err := node.NewHandler(r).GetSession(ctx, nodeapi.GetSessionRequestObject{ID: sess.ID})

	require.ErrorIs(t, err, wantErr)
	require.Nil(t, response)
}

func TestHandler_GetSession_MissingEnforcer(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	sess := seedSession(t, home, "session-consumption", "peer-consumption")
	sess.Signer = home.DaemonKey.PublicKey().String()
	require.NoError(t, home.DB.WithContext(t.Context()).Save(sess).Error)
	require.NoError(t, home.DB.WithContext(t.Context()).Create(&node.Position{Address: sess.Subject}).Error)

	response, err := node.NewHandler(r).GetSession(
		newSignedContext(t, home.DaemonKey.PublicKey()),
		nodeapi.GetSessionRequestObject{ID: sess.ID},
	)

	require.ErrorContains(t, err, "active session has no meter")
	require.Nil(t, response)
}

func TestHandler_GetSessionEvents_CancelsBlockedWrite(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	sess := seedSession(t, home, "session-events", "peer-events")
	sess.Signer = home.OwnerAddress.String()
	require.NoError(t, home.DB.WithContext(t.Context()).Save(sess).Error)

	ctx, cancel := context.WithCancel(newSignedContext(t, home.OwnerAddress))
	defer cancel()

	response, err := node.NewHandler(r).GetSessionEvents(ctx, nodeapi.GetSessionEventsRequestObject{ID: sess.ID})
	require.NoError(t, err)

	stream, ok := response.(nodeapi.EventsResponse)
	require.True(t, ok)
	reader, ok := stream.Body.(io.ReadCloser)
	require.True(t, ok)
	t.Cleanup(func() { require.NoError(t, reader.Close()) })

	node.Publish(r, sess.ID, api.EventComment("heartbeat"))

	buffer := make([]byte, 1)
	_, err = reader.Read(buffer)
	require.NoError(t, err)
	cancel()
	require.Eventually(t, func() bool {
		return node.SubscriberCount(r, sess.ID) == 0
	}, time.Second, time.Millisecond)

	_, err = reader.Read(buffer)
	require.ErrorIs(t, err, context.Canceled)
}

func TestHandler_GetSessionEvents_ConcurrentClose(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	sess := seedSession(t, home, "session-events", "peer-events")
	sess.Signer = home.OwnerAddress.String()
	require.NoError(t, home.DB.WithContext(t.Context()).Save(sess).Error)

	var closed atomic.Bool

	require.NoError(t, home.DB.Callback().Query().After("gorm:query").Register("test:close", func(db *gorm.DB) {
		if _, ok := db.Statement.Dest.(*node.Session); ok && !closed.Swap(true) {
			require.NoError(t, node.CloseSession(t.Context(), r, sess, "closed by client"))
		}
	}))
	response, err := node.NewHandler(r).GetSessionEvents(
		newSignedContext(t, home.OwnerAddress), nodeapi.GetSessionEventsRequestObject{ID: sess.ID},
	)
	require.NoError(t, err)

	events, ok := response.(nodeapi.EventsResponse)
	require.True(t, ok)

	decoder := api.NewEventDecoder(events.Body)
	frame, err := decoder.Next()
	require.NoError(t, err)
	require.Equal(t, nodeapi.EventClosed, frame.Name)

	_, err = decoder.Next()
	require.ErrorIs(t, err, io.EOF)
	require.Eventually(t, func() bool {
		return node.SubscriberCount(r, sess.ID) == 0
	}, time.Second, time.Millisecond)
}

func TestHandler_GetSessionEvents_IsolatesReplacement(t *testing.T) {
	t.Parallel()

	r, home, state := newRuntime(t)
	address := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
	_, chainNode := node.ChainState(r)
	terms := session.Terms{Basis: session.BasisPerGB, Price: chainNode.ChannelPricePerGB}
	paid := session.Owed(terms.Basis, terms.Price, 100_000_000)
	req := voucherRequest(t, home, address, terms, paid)
	first, err := node.Admit(t.Context(), r, home.OwnerAddress, req)
	require.NoError(t, err)
	old, _, err := node.OpenSession(t.Context(), r, first, peerRequestFixture())
	require.NoError(t, err)
	oldResponse, err := node.NewHandler(r).GetSessionEvents(
		newSignedContext(t, home.OwnerAddress), nodeapi.GetSessionEventsRequestObject{ID: old.ID},
	)
	require.NoError(t, err)

	oldStream, ok := oldResponse.(nodeapi.EventsResponse)
	require.True(t, ok)
	oldReader, ok := oldStream.Body.(io.ReadCloser)
	require.True(t, ok)
	t.Cleanup(func() { require.NoError(t, oldReader.Close()) })
	node.SetService(r, &failingRemoveOnceServer{Server: sdktest.NewFakeServer(state)})
	next, err := node.Admit(t.Context(), r, home.OwnerAddress, req)
	require.NoError(t, err)
	sess, _, err := node.OpenSession(t.Context(), r, next, peerRequestFixture())
	require.NoError(t, err)
	response, err := node.NewHandler(r).GetSessionEvents(
		newSignedContext(t, home.OwnerAddress), nodeapi.GetSessionEventsRequestObject{ID: sess.ID},
	)
	require.NoError(t, err)

	stream, ok := response.(nodeapi.EventsResponse)
	require.True(t, ok)
	reader, ok := stream.Body.(io.ReadCloser)
	require.True(t, ok)
	t.Cleanup(func() { require.NoError(t, reader.Close()) })
	state.SetStats(service.PeerID(sess.PeerID), service.PeerStats{RxBytes: 50_000_000})
	require.NoError(t, node.SyncUsage(t.Context(), r))

	oldDecoder := api.NewEventDecoder(oldReader)
	frame, err := oldDecoder.Next()
	require.NoError(t, err)
	require.Equal(t, nodeapi.EventClosed, frame.Name)

	_, err = oldDecoder.Next()
	require.ErrorIs(t, err, io.EOF)

	decoder := api.NewEventDecoder(reader)
	frame = readHandlerEvent(t, decoder)
	require.Equal(t, nodeapi.EventVoucherRequest, frame.Name)
	require.NoError(t, home.DB.WithContext(t.Context()).First(sess, "id = ?", sess.ID).Error)
	require.Equal(t, node.SessionStatusActive, sess.Status)
	require.NoError(t, node.CloseSession(t.Context(), r, sess, "closed by client"))

	frame = readHandlerEvent(t, decoder)
	require.Equal(t, nodeapi.EventClosed, frame.Name)

	_, err = decoder.Next()
	require.ErrorIs(t, err, io.EOF)
}

func TestHandler_GetSessionEvents_RejectsOtherSigner(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	sess := seedSession(t, home, uuid.NewString(), "peer-events")
	sess.Signer = home.OwnerAddress.String()
	require.NoError(t, home.DB.WithContext(t.Context()).Save(sess).Error)
	client := newHandlerClient(t, startHandlerServer(t, r), home.DaemonKey)
	response, err := client.GetSessionEvents(t.Context(), sess.ID)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, response.Body.Close()) })

	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
}

func TestHandler_GetSessionEvents_StreamsRequestAndClosed(t *testing.T) {
	t.Parallel()

	r, home, state := newRuntime(t)
	home.Loaded.Node.Sessions.HeartbeatSeconds = 1
	channel := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
	_, chainNode := node.ChainState(r)
	terms := session.Terms{Basis: session.BasisPerGB, Price: chainNode.ChannelPricePerGB}
	paid := session.Owed(terms.Basis, terms.Price, 100_000_000)
	admitted, err := node.Admit(t.Context(), r, home.OwnerAddress, voucherRequest(t, home, channel, terms, paid))
	require.NoError(t, err)
	sess, _, err := node.OpenSession(t.Context(), r, admitted, peerRequestFixture())
	require.NoError(t, err)
	client := newHandlerClient(t, startHandlerServer(t, r), home.OwnerKey)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	response, err := client.GetSessionEvents(ctx, sess.ID)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, response.Body.Close()) })

	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
	decoder := api.NewEventDecoder(response.Body)
	frame, err := decoder.Next()
	require.NoError(t, err)
	require.True(t, frame.IsComment)
	state.SetStats(service.PeerID(sess.PeerID), service.PeerStats{RxBytes: 50_000_000})
	require.NoError(t, node.SyncUsage(t.Context(), r))

	frame = readHandlerEvent(t, decoder)
	require.Equal(t, nodeapi.EventVoucherRequest, frame.Name)

	var request nodeapi.VoucherRequestEvent
	require.NoError(t, json.Unmarshal(frame.Data, &request))
	require.Equal(t, paid, request.Ledger.CumulativeBaseUnits)
	require.NoError(t, node.CloseSession(t.Context(), r, sess, "closed by client"))

	frame = readHandlerEvent(t, decoder)
	require.Equal(t, nodeapi.EventClosed, frame.Name)

	_, err = decoder.Next()
	require.ErrorIs(t, err, io.EOF)
	closed, err := client.GetSessionEvents(t.Context(), sess.ID)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, closed.Body.Close()) })

	require.Equal(t, http.StatusOK, closed.StatusCode)
	decoder = api.NewEventDecoder(closed.Body)

	frame = readHandlerEvent(t, decoder)
	require.Equal(t, nodeapi.EventClosed, frame.Name)

	var reason nodeapi.ClosedEvent
	require.NoError(t, json.Unmarshal(frame.Data, &reason))
	require.Equal(t, nodeapi.ClosedReasonClient, reason.Reason)

	_, err = decoder.Next()
	require.ErrorIs(t, err, io.EOF)
}

func TestHandler_OpenSession_ConflictingPeer(t *testing.T) {
	t.Parallel()

	r, home, state := newRuntime(t)
	node.SetService(r, &fakePinnedPeerID{Server: sdktest.NewFakeServer(state)})
	channel := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeStream)
	req := nodeapi.OpenSessionRequest{
		Credential: nodeapi.Credential{
			Channel: &channel,
			Type:    nodeapi.CredentialTypeChannel,
		},
		Peer: peerRequestFixture(),
	}
	response, err := node.NewHandler(r).OpenSession(
		newSignedContext(t, home.OwnerAddress), nodeapi.OpenSessionRequestObject{Body: &req},
	)
	require.NoError(t, err)
	require.NotNil(t, response)

	ownerB, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	channelB := seedChannel(t, r, home, ownerB, dvpn.PositionModeStream)
	req.Credential.Channel = &channelB
	response, err = node.NewHandler(r).OpenSession(
		newSignedContext(t, ownerB.PublicKey()), nodeapi.OpenSessionRequestObject{Body: &req},
	)
	require.ErrorIs(t, err, api.ErrConflict)
	require.Nil(t, response)
	require.Empty(t, state.RemovedPeers())
}

func TestHandler_OpenSession_ConsumptionReadFailure(t *testing.T) {
	t.Parallel()

	r, home, state := newRuntime(t)
	channel := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeStream)
	wantErr := errors.New("consumption read failed")

	require.NoError(t, home.DB.Callback().Query().After("gorm:query").
		Register("test:consumption_failure", func(db *gorm.DB) {
			_, isSession := db.Statement.Dest.(*node.Session)
			if isSession && strings.Contains(db.Statement.SQL.String(), "subject =") {
				db.Error = wantErr
			}
		}))
	response, err := node.NewHandler(r).OpenSession(
		newSignedContext(t, home.OwnerAddress),
		nodeapi.OpenSessionRequestObject{
			Body: &nodeapi.OpenSessionRequest{
				Credential: nodeapi.Credential{Channel: &channel, Type: nodeapi.CredentialTypeChannel},
				Peer:       peerRequestFixture(),
			},
		},
	)

	require.ErrorIs(t, err, wantErr)
	require.Nil(t, response)
	require.Len(t, state.AddedPeers(), 1)
}

func TestHandler_Routes(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	require.NoError(t, node.RefreshMetadata(t.Context(), r))

	server := startHandlerServer(t, r)

	unsigned, err := nodeapi.NewInsecureClient(server.URL, 5*time.Second)
	require.NoError(t, err)
	health, err := unsigned.GetHealthWithResponse(t.Context())

	require.NoError(t, err)
	require.NotNil(t, health.JSON200)
	require.Equal(t, nodeapi.HealthStatusOK, health.JSON200.Status)
	require.Equal(t, version.Tag, health.JSON200.Version)
	metadata, err := unsigned.GetMetadataWithResponse(t.Context())

	require.NoError(t, err)
	require.NotNil(t, metadata.JSON200)

	httpClient := api.NewInsecureHTTPClient(5 * time.Second)
	httpClient.Transport = &api.SignedTransport{Base: httpClient.Transport, Signer: home.DaemonKey}
	signed, err := nodeapi.NewClientWithResponses(server.URL, nodeapi.WithHTTPClient(httpClient))
	require.NoError(t, err)
	session, err := signed.GetSessionWithResponse(t.Context(), uuid.NewString())

	require.NoError(t, err)
	require.NotNil(t, session.JSONDefault)
	require.Equal(t, api.CodeSessionNotFound, session.JSONDefault.Error.Code)
}

func TestHandler_SendChannelVoucher_RejectsReplacedPosition(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	client := &fakeSlotRPCClient{JSONRPCClient: jsonrpc.NewClient(home.RPC.Server.URL)}
	client.slot.Store(100)
	home.ProgramClient.RPC = rpc.NewWithCustomRPCClient(client)
	address := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
	seedPosition(t, r, home, address, 100)
	signed := voucher.Sign(home.OwnerKey, voucher.ChannelMessage(address, channelOpenedUnix, 200))

	var reopened atomic.Bool

	require.NoError(t, home.DB.Callback().Update().Before("gorm:begin_transaction").
		Register("test:reopen", func(db *gorm.DB) {
			updates, ok := db.Statement.Dest.(map[string]any)
			if !ok || updates["signature"] == nil || updates["chain_slot"] != nil {
				return
			}

			if reopened.Swap(true) {
				return
			}

			_, channel := channelFixture(
				t, r, home, home.OwnerKey, home.OwnerAddress, home.OwnerAddress, dvpn.PositionModeVoucher,
			)
			channel.OpenedAt++

			client.slot.Store(101)
			home.RPC.SetAccount(address, sdktest.Account{
				Data:  sdktest.EncodeAccount(dvpn.ChannelAccountDiscriminator, channel),
				Owner: home.ProgramClient.ProgramID,
			})
			_, err := node.NewHandler(r).GetChannel(
				newSignedContext(t, home.OwnerAddress), nodeapi.GetChannelRequestObject{Channel: address},
			)
			require.NoError(t, err)
		}))
	response, err := node.NewHandler(r).SendChannelVoucher(newSignedContext(t, home.OwnerAddress),
		nodeapi.SendChannelVoucherRequestObject{
			Channel: address,
			Body: &nodeapi.SendVoucherRequest{Voucher: nodeapi.Voucher{
				Cumulative: 200,
				Signature:  signed.Signature[:],
			}},
		})
	require.ErrorIs(t, err, api.ErrChannelNotOpen)
	require.Nil(t, response)

	var stored node.Position
	require.NoError(t, home.DB.WithContext(t.Context()).First(&stored, "address = ?", address.String()).Error)
	require.Equal(t, channelOpenedUnix+1, stored.OpenedUnix)
	require.Zero(t, stored.Cumulative)
	require.Empty(t, stored.Signature)
}

func TestHandler_SendLeaseVoucher_ElapsedConsumption(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	address := solana.PublicKey{55}
	_, chainNode := node.ChainState(r)
	openedUnix := time.Now().Unix() - 3600
	lease := dvpn.Lease{
		Escrow: 10_000, Node: home.NodeAddress, NodeOpenedAt: chainNode.OpenedAt,
		OpenedAt: openedUnix, Owner: home.OwnerAddress, RatePerHour: 3600,
		Status: dvpn.PositionStatusOpen, VoucherSigner: home.OwnerAddress,
	}
	home.RPC.SetAccount(address, sdktest.Account{
		Data: sdktest.EncodeAccount(dvpn.LeaseAccountDiscriminator, lease), Owner: home.ProgramClient.ProgramID,
	})
	require.NoError(t, home.DB.WithContext(t.Context()).Create(&node.Position{
		Address: address.String(), Kind: node.KindLease, NodeOpenedUnix: chainNode.OpenedAt,
		OpenedUnix: openedUnix, RatePerHour: 3600, Settled: 7,
	}).Error)
	signed := voucher.Sign(home.OwnerKey, voucher.LeaseMessage(address, openedUnix, 5000))
	response, err := node.NewHandler(r).SendLeaseVoucher(newSignedContext(t, home.OwnerAddress),
		nodeapi.SendLeaseVoucherRequestObject{
			Lease: address, Body: &nodeapi.SendVoucherRequest{
				Voucher: nodeapi.Voucher{Cumulative: 5000, Signature: signed.Signature[:]},
			},
		})
	require.NoError(t, err)

	result, ok := response.(nodeapi.SendLeaseVoucher200JSONResponse)
	require.True(t, ok)
	require.GreaterOrEqual(t, result.Consumption.Usage.Seconds, uint64(3600))
	require.LessOrEqual(t, result.Consumption.Usage.Seconds, uint64(time.Now().Unix()-openedUnix))
	require.Equal(t, result.Consumption.Usage.Seconds, result.Consumption.OwedBaseUnits)
	require.EqualValues(t, 7, result.Consumption.SettledBaseUnits)
	require.EqualValues(t, 3600, result.Ledger.Price)
}

func newHandlerClient(t *testing.T, server *httptest.Server, key solana.PrivateKey) *nodeapi.ClientWithResponses {
	t.Helper()

	client, err := nodeapi.NewPinnedClient(server.URL, api.Fingerprint(server.Certificate()), key, 0)
	require.NoError(t, err)

	return client
}

func readHandlerEvent(t *testing.T, decoder *api.EventDecoder) api.Event {
	t.Helper()

	for {
		event, err := decoder.Next()
		require.NoError(t, err)

		if !event.IsComment {
			return event
		}
	}
}

func seedHandlerLease(t *testing.T, r *node.Runtime, home testutil.Home) solana.PublicKey {
	t.Helper()

	address := solana.PublicKey{55}
	_, chainNode := node.ChainState(r)
	lease := dvpn.Lease{
		Escrow:        10_000,
		Node:          home.NodeAddress,
		NodeOpenedAt:  chainNode.OpenedAt,
		OpenedAt:      time.Now().Unix(),
		Owner:         home.OwnerAddress,
		RatePerHour:   9,
		Status:        dvpn.PositionStatusOpen,
		VoucherSigner: home.OwnerAddress,
	}
	home.RPC.SetAccount(address, sdktest.Account{
		Data:  sdktest.EncodeAccount(dvpn.LeaseAccountDiscriminator, lease),
		Owner: home.ProgramClient.ProgramID,
	})
	require.NoError(t, home.DB.WithContext(t.Context()).Create(&node.Position{
		Address:        address.String(),
		Kind:           node.KindLease,
		NodeOpenedUnix: chainNode.OpenedAt,
		OpenedUnix:     lease.OpenedAt,
		RatePerHour:    7,
	}).Error)

	return address
}

func startHandlerServer(t *testing.T, r *node.Runtime) *httptest.Server {
	t.Helper()

	engine := api.NewEngine(api.EngineOptions{
		Logger:         zerolog.Nop(),
		StreamingPaths: nodeapi.StreamingPaths(),
	})
	nonces := auth.NewNonceCache(auth.RequestNonceWindow)
	ipLimiter := api.NewLimiter(100, 100)
	signerLimiter := api.NewLimiter(100, 100)

	t.Cleanup(nonces.Stop)
	t.Cleanup(ipLimiter.Stop)
	t.Cleanup(signerLimiter.Stop)
	nodeapi.RegisterRoutes(engine, node.NewHandler(r), nonces, ipLimiter, signerLimiter)
	server := httptest.NewTLSServer(engine)
	t.Cleanup(server.Close)

	return server
}
