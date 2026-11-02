//go:build linux

package node_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/sentinel-official/sentinel-dvpnd/internal/node"
	"github.com/sentinel-official/sentinel-dvpnd/internal/store"
	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
	"github.com/sentinel-official/sentinel-go-sdk/v3/voucher"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestRuntime_AcceptVoucher_CountsAccepted(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	r, home, _ := newRuntimeWithRegistry(t, reg)
	channel := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
	_, chainNode := node.ChainState(r)
	terms := session.Terms{Basis: session.BasisPerGB, Price: chainNode.ChannelPricePerGB}
	block := session.Owed(terms.Basis, terms.Price, session.BytesPerGB)

	submitted := voucherRequest(t, home, channel, terms, block).Voucher
	_, err := node.AcceptChannelVoucher(t.Context(), r, channel, home.OwnerAddress, *submitted)
	require.NoError(t, err)

	families, err := reg.Gather()
	require.NoError(t, err)

	var got float64

	for _, family := range families {
		if family.GetName() != "sentinel_dvpnd_vouchers_total" {
			continue
		}

		for _, metric := range family.GetMetric() {
			got += metric.GetCounter().GetValue()
		}
	}

	require.InDelta(t, 1, got, 0)
}

func TestRuntime_Admit_AuthorizesPartiesThroughGrants(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	opener, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	voucherSigner, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	grantee, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	channel := seedChannelAccount(
		t,
		r,
		home,
		home.OwnerKey,
		opener.PublicKey(),
		voucherSigner.PublicKey(),
		dvpn.PositionModeVoucher,
		home.ProgramClient.ProgramID,
	)
	_, chainNode := node.ChainState(r)
	terms := session.Terms{Basis: session.BasisPerGB, Price: chainNode.ChannelPricePerGB}
	owed := session.Owed(terms.Basis, terms.Price, session.BytesPerGB)
	signed := voucher.Sign(voucherSigner, voucher.ChannelMessage(channel, channelOpenedUnix, owed))
	req := nodeapi.OpenSessionRequest{
		Credential: nodeapi.Credential{Channel: &channel, Type: nodeapi.CredentialTypeChannel},
		Terms:      &terms,
		Voucher:    &nodeapi.Voucher{Cumulative: signed.CumulativeAmount, Signature: signed.Signature[:]},
	}

	_, err = node.Admit(t.Context(), r, voucherSigner.PublicKey(), req)
	require.ErrorIs(t, err, api.ErrUnauthorized)

	_, err = node.Admit(t.Context(), r, opener.PublicKey(), req)
	require.ErrorIs(t, err, api.ErrUnauthorized)

	require.NoError(t, home.RPC.SetGrant(
		home.ProgramClient.ProgramID,
		home.OwnerAddress,
		grantee.PublicKey(),
		solana.PublicKey{},
		dvpn.PermOpenChannel,
	))
	admitted, err := node.Admit(t.Context(), r, grantee.PublicKey(), req)
	require.NoError(t, err)
	require.Equal(t, channel.String(), admitted.Subject())
}

func TestRuntime_Admit_ConcurrentCloseUsesSettledCredit(t *testing.T) {
	t.Parallel()

	r, home, state := newRuntime(t)
	channel := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
	_, chainNode := node.ChainState(r)
	terms := session.Terms{
		Basis: session.BasisPerGB,
		Price: chainNode.ChannelPricePerGB,
	}
	block := session.Owed(terms.Basis, terms.Price, 100_000_000)
	req := voucherRequest(t, home, channel, terms, block)
	admitted, err := node.Admit(t.Context(), r, home.OwnerAddress, req)
	require.NoError(t, err)
	sess, _, err := node.OpenSession(t.Context(), r, admitted, peerRequestFixture())
	require.NoError(t, err)
	state.SetStats(service.PeerID(sess.PeerID), service.PeerStats{RxBytes: 50_000_001})

	var closed atomic.Bool

	require.NoError(t, home.DB.Callback().Update().After("gorm:update").Register("test:close", func(db *gorm.DB) {
		updates, ok := db.Statement.Dest.(map[string]any)
		if !ok || updates["chain_slot"] == nil || closed.Swap(true) {
			return
		}

		require.NoError(t, node.CloseSession(t.Context(), r, sess, "concurrent close"))
	}))

	_, err = node.Admit(t.Context(), r, home.OwnerAddress, req)
	require.True(t, closed.Load())
	require.ErrorIs(t, err, api.ErrPrepayBelowMinimum)
	require.Equal(t, session.Owed(terms.Basis, terms.Price, 50_000_001), loadSettled(t, home, channel))
	require.Equal(t, []service.PeerID{service.PeerID(sess.PeerID)}, state.RemovedPeers())
}

func TestRuntime_Admit_FreeChannelRetainsDebtBoundary(t *testing.T) {
	t.Parallel()

	for name, amounts := range map[string][2]uint64{
		"free":         {0, 0},
		"covered debt": {5, 5},
		"unpaid debt":  {0, 5},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r, home, state := newRuntime(t)
			_, chainNode := node.ChainState(r)
			chainNode.ChannelPricePerGB = 0
			node.SetChainNode(r, chainNode)
			address := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
			seedPosition(t, r, home, address, 0)
			require.NoError(t, home.DB.WithContext(t.Context()).Model(&node.Position{}).
				Where("address = ?", address.String()).Update("settled", amounts[1]).Error)

			terms := session.Terms{Basis: session.BasisPerGB}

			admitted, err := node.Admit(t.Context(), r, home.OwnerAddress,
				voucherRequest(t, home, address, terms, amounts[0]))
			if amounts[0] < amounts[1] {
				require.ErrorIs(t, err, api.ErrPrepayBelowMinimum)

				return
			}

			require.NoError(t, err)
			sess, _, err := node.OpenSession(t.Context(), r, admitted, peerRequestFixture())
			require.NoError(t, err)
			state.SetStats(service.PeerID(sess.PeerID), service.PeerStats{RxBytes: session.BytesPerGB})
			require.NoError(t, node.SyncUsage(t.Context(), r))
			require.NoError(t, home.DB.WithContext(t.Context()).First(sess, "id = ?", sess.ID).Error)
			require.Equal(t, node.SessionStatusActive, sess.Status)
			require.Equal(t, amounts[1], loadSettled(t, home, address))
		})
	}
}

func TestRuntime_Admit_FreeLeasePermitsTicketSession(t *testing.T) {
	t.Parallel()

	r, home, state := newRuntime(t)
	_, chainNode := node.ChainState(r)
	chainNode.LeasePricePerHour = 0
	node.SetChainNode(r, chainNode)

	provider := solana.PublicKey{61}
	home.RPC.SetAccount(provider, sdktest.Account{
		Data:  sdktest.EncodeAccount(dvpn.ProviderAccountDiscriminator, dvpn.Provider{Owner: home.OwnerAddress}),
		Owner: home.ProgramClient.ProgramID,
	})
	lease := dvpn.Lease{
		Node:         home.NodeAddress,
		NodeOpenedAt: chainNode.OpenedAt,
		OpenedAt:     time.Now().Unix() - 3600,
		Owner:        home.OwnerAddress,
		Provider:     provider,
		Status:       dvpn.PositionStatusOpen,
	}
	home.RPC.SetAccount(solana.PublicKey{62}, sdktest.Account{
		Data:  sdktest.EncodeAccount(dvpn.LeaseAccountDiscriminator, lease),
		Owner: home.ProgramClient.ProgramID,
	})
	encoded := auth.SignTicket(home.OwnerKey, auth.Ticket{
		DataCapBytes: 100,
		ExpiresUnix:  time.Now().Unix() + 3600,
		Node:         home.NodeAddress,
		Provider:     provider,
		User:         home.OwnerAddress,
		Version:      auth.TicketVersion,
	}).Encode()
	admitted, err := node.Admit(t.Context(), r, home.OwnerAddress, nodeapi.OpenSessionRequest{
		Credential: nodeapi.Credential{Ticket: &encoded, Type: nodeapi.CredentialTypeTicket},
	})
	require.NoError(t, err)
	sess, _, err := node.OpenSession(t.Context(), r, admitted, peerRequestFixture())
	require.NoError(t, err)
	state.SetStats(service.PeerID(sess.PeerID), service.PeerStats{RxBytes: 10})
	require.NoError(t, node.SyncUsage(t.Context(), r))
	require.NoError(t, home.DB.WithContext(t.Context()).First(sess, "id = ?", sess.ID).Error)
	require.Equal(t, node.SessionStatusActive, sess.Status)
}

func TestRuntime_Admit_RejectedPrepayKeepsActiveSession(t *testing.T) {
	t.Parallel()

	r, home, state := newRuntime(t)
	channel := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
	_, chainNode := node.ChainState(r)
	terms := session.Terms{
		Basis: session.BasisPerGB,
		Price: chainNode.ChannelPricePerGB,
	}
	block := session.Owed(terms.Basis, terms.Price, 100_000_000)
	req := voucherRequest(t, home, channel, terms, block)
	admitted, err := node.Admit(t.Context(), r, home.OwnerAddress, req)
	require.NoError(t, err)
	sess, _, err := node.OpenSession(t.Context(), r, admitted, peerRequestFixture())
	require.NoError(t, err)
	state.SetStats(service.PeerID(sess.PeerID), service.PeerStats{RxBytes: 50_000_001})

	_, err = node.Admit(t.Context(), r, home.OwnerAddress, req)
	require.ErrorIs(t, err, api.ErrPrepayBelowMinimum)

	var stored node.Session
	require.NoError(t, home.DB.WithContext(t.Context()).First(&stored, "id = ?", sess.ID).Error)
	require.Equal(t, node.SessionStatusActive, stored.Status)
	require.Empty(t, state.RemovedPeers())
	require.Zero(t, loadSettled(t, home, channel))
}

func TestRuntime_Admit_RejectsChannelOfOtherProgram(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	foreign, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	channel := seedChannelOwnedBy(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher, foreign.PublicKey())
	_, chainNode := node.ChainState(r)
	terms := session.Terms{Basis: session.BasisPerGB, Price: chainNode.ChannelPricePerGB}
	owed := session.Owed(terms.Basis, terms.Price, session.BytesPerGB)
	signed := voucher.Sign(home.OwnerKey, voucher.ChannelMessage(channel, channelOpenedUnix, owed))
	req := nodeapi.OpenSessionRequest{
		Credential: nodeapi.Credential{Channel: &channel, Type: nodeapi.CredentialTypeChannel},
		Terms:      &terms,
		Voucher:    &nodeapi.Voucher{Cumulative: signed.CumulativeAmount, Signature: signed.Signature[:]},
	}
	_, err = node.Admit(t.Context(), r, home.OwnerAddress, req)
	require.ErrorIs(t, err, api.ErrChannelNotOpen)
}

func TestRuntime_Admit_RejectsOwnerSignedVoucherOnDelegatedChannel(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	opener, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	channel := seedDelegatedChannel(t, r, home, home.OwnerKey, opener.PublicKey())
	_, chainNode := node.ChainState(r)
	terms := session.Terms{Basis: session.BasisPerGB, Price: chainNode.ChannelPricePerGB}
	owed := session.Owed(terms.Basis, terms.Price, session.BytesPerGB)
	message := voucher.ChannelMessage(channel, channelOpenedUnix, owed)
	ownerSigned := voucher.Sign(home.OwnerKey, message)
	req := nodeapi.OpenSessionRequest{
		Credential: nodeapi.Credential{Channel: &channel, Type: nodeapi.CredentialTypeChannel},
		Terms:      &terms,
		Voucher:    &nodeapi.Voucher{Cumulative: ownerSigned.CumulativeAmount, Signature: ownerSigned.Signature[:]},
	}

	_, err = node.Admit(t.Context(), r, home.OwnerAddress, req)
	require.ErrorIs(t, err, api.ErrInvalidVoucherSignature)

	require.NoError(t, home.RPC.SetGrant(
		home.ProgramClient.ProgramID,
		home.OwnerAddress,
		opener.PublicKey(),
		solana.PublicKey{},
		dvpn.PermOpenChannel,
	))

	openerSigned := voucher.Sign(opener, message)
	req.Voucher.Signature = openerSigned.Signature[:]
	admitted, err := node.Admit(t.Context(), r, opener.PublicKey(), req)
	require.NoError(t, err)
	require.Equal(t, channel.String(), admitted.Subject())
}

func TestRuntime_Admit_RejectsRepricedTerms(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	channel := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
	_, chainNode := node.ChainState(r)
	terms := session.Terms{Basis: session.BasisPerGB, Price: chainNode.ChannelPricePerGB + 1}
	block := session.Owed(terms.Basis, terms.Price, 100_000_000)

	_, err := node.Admit(t.Context(), r, home.OwnerAddress, voucherRequest(t, home, channel, terms, block))

	require.ErrorIs(t, err, api.ErrTermsRejected)
}

func TestRuntime_Admit_RejectsStaleNode(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	channel := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeStream)
	_, chainNode := node.ChainState(r)
	chainNode.OpenedAt++
	node.SetChainNode(r, chainNode)

	req := nodeapi.OpenSessionRequest{
		Credential: nodeapi.Credential{Channel: &channel, Type: nodeapi.CredentialTypeChannel},
	}
	_, err := node.Admit(t.Context(), r, home.OwnerAddress, req)
	require.ErrorIs(t, err, api.ErrChannelNodeStale)
}

func TestRuntime_Admit_ReplacesActiveSession(t *testing.T) {
	t.Parallel()

	r, home, state := newRuntime(t)
	channel := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
	_, chainNode := node.ChainState(r)
	terms := session.Terms{Basis: session.BasisPerGB, Price: chainNode.ChannelPricePerGB}
	block := session.Owed(terms.Basis, terms.Price, session.BytesPerGB)
	first, err := node.Admit(t.Context(), r, home.OwnerAddress, voucherRequest(t, home, channel, terms, block))
	require.NoError(t, err)
	sess, _, err := node.OpenSession(t.Context(), r, first, peerRequestFixture())
	require.NoError(t, err)
	state.SetStats(service.PeerID(sess.PeerID), service.PeerStats{RxBytes: session.BytesPerGB / 2})

	second, err := node.Admit(t.Context(), r, home.OwnerAddress, voucherRequest(t, home, channel, terms, 2*block))
	require.NoError(t, err)
	next, _, err := node.OpenSession(t.Context(), r, second, peerRequestFixture())
	require.NoError(t, err)

	var old node.Session
	require.NoError(t, home.DB.WithContext(t.Context()).First(&old, "id = ?", sess.ID).Error)
	require.Equal(t, node.SessionStatusClosed, old.Status)
	require.NotEqual(t, sess.ID, next.ID)
	require.Equal(t, []service.PeerID{service.PeerID(sess.PeerID)}, state.RemovedPeers())

	halfOwed := session.Owed(terms.Basis, terms.Price, session.BytesPerGB/2)
	require.Equal(t, halfOwed, loadSettled(t, home, channel))

	meter, ok := second.Enforcer().(*session.Meter)
	require.True(t, ok)
	require.Equal(t, 2*block-halfOwed, meter.Headroom(session.Usage{}))
}

func TestRuntime_Admit_TicketRequestRefusesPrivateProvider(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32

	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(server.Close)

	fakeRPC := sdktest.NewFakeRPC(t)
	home := testutil.NewNodeHome(t, fakeRPC)
	home.Loaded.File.Peer.AllowPrivate = false
	r, err := node.New(
		home.Loaded, home.DB, home.DaemonKey, home.ProgramClient, home.Sender, zerolog.Nop(), prometheus.NewRegistry(),
	)
	require.NoError(t, err)
	require.NoError(t, store.Migrate(t.Context(), home.DB, r.Models()...))
	require.NoError(t, node.SyncChain(t.Context(), r))

	var uri [128]uint8

	copy(uri[:], server.URL)

	provider := solana.PublicKey{63}
	home.RPC.SetAccount(provider, sdktest.Account{
		Data: sdktest.EncodeAccount(dvpn.ProviderAccountDiscriminator, dvpn.Provider{
			Owner:  home.OwnerAddress,
			URI:    uri,
			URILen: uint8(len(server.URL)),
		}),
		Owner: home.ProgramClient.ProgramID,
	})
	encoded := auth.SignTicketRequest(home.OwnerKey, auth.TicketRequest{
		Node:          home.NodeAddress,
		Provider:      provider,
		RequestedUnix: time.Now().Unix(),
		Version:       auth.TicketVersion,
	}).Encode()

	_, err = node.Admit(t.Context(), r, home.OwnerAddress, nodeapi.OpenSessionRequest{
		Credential: nodeapi.Credential{TicketRequest: &encoded, Type: nodeapi.CredentialTypeTicketRequest},
	})
	require.ErrorIs(t, err, api.ErrInvalidTicket)
	require.Zero(t, hits.Load())
}

func TestRuntime_Admit_UsesConcurrentVoucher(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	channel := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
	_, chainNode := node.ChainState(r)
	terms := session.Terms{Basis: session.BasisPerGB, Price: chainNode.ChannelPricePerGB}

	seedPosition(t, r, home, channel, 500)

	var advanced atomic.Bool

	require.NoError(t, home.DB.Callback().Update().Before("gorm:update").Register("test:topup", func(db *gorm.DB) {
		updates, ok := db.Statement.Dest.(map[string]any)
		if !ok || updates["cumulative"] != uint64(1_000) || advanced.Swap(true) {
			return
		}

		require.NoError(t, db.Session(&gorm.Session{NewDB: true}).Model(&node.Position{}).
			Where("address = ?", channel.String()).Update("cumulative", 2_000).Error)
	}))

	admitted, err := node.Admit(t.Context(), r, home.OwnerAddress, voucherRequest(t, home, channel, terms, 1_000))
	require.NoError(t, err)
	_, _, err = node.OpenSession(t.Context(), r, admitted, peerRequestFixture())
	require.NoError(t, err)

	meter, ok := admitted.Enforcer().(*session.Meter)
	require.True(t, ok)
	require.EqualValues(t, 2_000, meter.Paid())
}

func TestRuntime_Admit_VerifiesVoucherWithVoucherSigner(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	voucherSigner, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	channel := seedChannelAccount(
		t,
		r,
		home,
		home.OwnerKey,
		home.OwnerKey.PublicKey(),
		voucherSigner.PublicKey(),
		dvpn.PositionModeVoucher,
		home.ProgramClient.ProgramID,
	)
	_, chainNode := node.ChainState(r)
	terms := session.Terms{Basis: session.BasisPerGB, Price: chainNode.ChannelPricePerGB}
	owed := session.Owed(terms.Basis, terms.Price, session.BytesPerGB)
	message := voucher.ChannelMessage(channel, channelOpenedUnix, owed)
	ownerSigned := voucher.Sign(home.OwnerKey, message)
	req := nodeapi.OpenSessionRequest{
		Credential: nodeapi.Credential{Channel: &channel, Type: nodeapi.CredentialTypeChannel},
		Terms:      &terms,
		Voucher:    &nodeapi.Voucher{Cumulative: ownerSigned.CumulativeAmount, Signature: ownerSigned.Signature[:]},
	}

	_, err = node.Admit(t.Context(), r, home.OwnerAddress, req)
	require.ErrorIs(t, err, api.ErrInvalidVoucherSignature)

	signerSigned := voucher.Sign(voucherSigner, message)
	req.Voucher.Signature = signerSigned.Signature[:]
	admitted, err := node.Admit(t.Context(), r, home.OwnerAddress, req)
	require.NoError(t, err)
	require.Equal(t, channel.String(), admitted.Subject())
}

func TestRuntime_Admit_VoucherChannel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                   string
		cumulativeBlocks       uint64
		settledBlocks          uint64
		settledBlocksMinusHalf bool
		settledExtra           uint64
		voucherBlocks          uint64
		voucherAboveEscrow     bool
		wantPaidBlocks         uint64
		wantErr                error
	}{
		{name: "fresh channel stores the voucher", voucherBlocks: 1, wantPaidBlocks: 1},
		{
			name:             "surplus admits a lower voucher unstored",
			cumulativeBlocks: 3,
			settledBlocks:    1,
			voucherBlocks:    1,
			wantPaidBlocks:   3,
		},
		{
			name:                   "half minimum headroom admitted",
			cumulativeBlocks:       3,
			settledBlocks:          3,
			settledBlocksMinusHalf: true,
			voucherBlocks:          1,
			wantPaidBlocks:         3,
		},
		{
			name:                   "below half minimum rejected",
			cumulativeBlocks:       3,
			settledBlocks:          3,
			settledBlocksMinusHalf: true,
			settledExtra:           1,
			voucherBlocks:          1,
			wantErr:                api.ErrPrepayBelowMinimum,
		},
		{
			name:               "voucher above escrow",
			voucherAboveEscrow: true,
			wantErr:            api.ErrVoucherExceedsEscrow,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r, home, _ := newRuntime(t)
			channel := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
			_, chainNode := node.ChainState(r)
			terms := session.Terms{Basis: session.BasisPerGB, Price: chainNode.ChannelPricePerGB}
			block := session.Owed(terms.Basis, terms.Price, 100_000_000)
			cumulative := tc.cumulativeBlocks * block

			settled := tc.settledBlocks * block
			if tc.settledBlocksMinusHalf {
				settled -= block / 2
			}

			settled += tc.settledExtra

			voucherAmount := tc.voucherBlocks * block
			if tc.voucherAboveEscrow {
				voucherAmount = channelEscrow + 1
			}

			if cumulative > 0 {
				seedPosition(t, r, home, channel, cumulative)
				require.NoError(t, home.DB.WithContext(t.Context()).Model(&node.Position{}).
					Where("address = ?", channel.String()).Update("settled", settled).Error)
			}

			req := voucherRequest(t, home, channel, terms, voucherAmount)

			admitted, err := node.Admit(t.Context(), r, home.OwnerAddress, req)

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				var stored node.Position
				require.NoError(t, home.DB.WithContext(t.Context()).First(&stored, "address = ?", channel.String()).Error)
				require.Equal(t, cumulative, stored.Cumulative)

				if cumulative == 0 {
					require.Empty(t, stored.Signature)
				} else {
					require.Equal(t, make([]byte, solana.SignatureLength), stored.Signature)
				}

				return
			}

			require.NoError(t, err)
			require.Equal(t, channel.String(), admitted.Subject())
			meter, ok := admitted.Enforcer().(*session.Meter)
			require.True(t, ok)
			require.Equal(t, tc.wantPaidBlocks*block, meter.Paid())
			_, _, err = node.OpenSession(t.Context(), r, admitted, peerRequestFixture())
			require.NoError(t, err)

			var position node.Position
			require.NoError(t, home.DB.WithContext(t.Context()).First(&position, "address = ?", channel.String()).Error)

			wantCumulative := max(cumulative, voucherAmount)
			require.Equal(t, wantCumulative, position.Cumulative)

			if cumulative == 0 {
				require.Equal(t, req.Voucher.Signature, position.Signature)
			} else {
				require.Equal(t, make([]byte, solana.SignatureLength), position.Signature)
			}
		})
	}
}

func TestRuntime_Admit_VoucherChannel_PerHourStaleSessionSettledOnce(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	channel := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
	_, chainNode := node.ChainState(r)
	terms := session.Terms{Basis: session.BasisPerHour, Price: chainNode.ChannelPricePerHour}
	block := session.Owed(terms.Basis, terms.Price, 60)
	seedPosition(t, r, home, channel, block)
	sess := seedSession(t, home, "session-stale-hourly", "peer-stale-hourly")
	sess.Basis = uint8(terms.Basis)
	sess.CreatedUnix = time.Now().Unix() - 15
	sess.Price = terms.Price
	sess.Subject = channel.String()
	require.NoError(t, home.DB.WithContext(t.Context()).Save(sess).Error)
	node.SetEnforcer(r, sess.ID, session.NewMeter(terms, block, 0))

	request := voucherRequest(t, home, channel, terms, block)
	first, err := node.Admit(t.Context(), r, home.OwnerAddress, request)
	require.NoError(t, err)

	var closed node.Session
	require.NoError(t, home.DB.WithContext(t.Context()).First(&closed, "id = ?", sess.ID).Error)
	require.Equal(t, node.SessionStatusClosed, closed.Status)
	require.GreaterOrEqual(t, closed.Seconds, uint64(15))
	settled := session.Owed(terms.Basis, terms.Price, closed.Seconds)
	require.Equal(t, settled, loadSettled(t, home, channel))

	firstMeter, ok := first.Enforcer().(*session.Meter)
	require.True(t, ok)

	firstHeadroom := firstMeter.Headroom(session.Usage{})
	require.GreaterOrEqual(t, firstHeadroom, block/2)
	require.Less(t, firstHeadroom, block)
	require.Equal(t, block-settled, firstHeadroom)

	second, err := node.Admit(t.Context(), r, home.OwnerAddress, request)
	require.NoError(t, err)
	require.Equal(t, settled, loadSettled(t, home, channel))

	secondMeter, ok := second.Enforcer().(*session.Meter)
	require.True(t, ok)
	require.Equal(t, block-settled, secondMeter.Headroom(session.Usage{}))

	var closedCount int64
	require.NoError(t, home.DB.WithContext(t.Context()).Model(&node.Session{}).
		Where("subject = ? AND status = ?", channel.String(), node.SessionStatusClosed).Count(&closedCount).Error)
	require.EqualValues(t, 1, closedCount)
}

func TestRuntime_Admit_VoucherChannel_StaleSessionSettledOnce(t *testing.T) {
	t.Parallel()

	r, home, state := newRuntime(t)
	channel := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
	_, chainNode := node.ChainState(r)
	terms := session.Terms{Basis: session.BasisPerGB, Price: chainNode.ChannelPricePerGB}
	block := session.Owed(terms.Basis, terms.Price, 100_000_000)
	seedPosition(t, r, home, channel, 2*block)
	sess := seedSession(t, home, "session-stale", "peer-stale")
	sess.Basis = uint8(terms.Basis)
	sess.Price = terms.Price
	sess.Subject = channel.String()
	require.NoError(t, home.DB.WithContext(t.Context()).Save(sess).Error)
	node.SetEnforcer(r, sess.ID, session.NewMeter(terms, 2*block, 0))
	state.SetStats(service.PeerID(sess.PeerID), service.PeerStats{RxBytes: 25_000_000})

	admitted, err := node.Admit(t.Context(), r, home.OwnerAddress, voucherRequest(t, home, channel, terms, 2*block))

	require.NoError(t, err)

	var closed node.Session
	require.NoError(t, home.DB.WithContext(t.Context()).First(&closed, "id = ?", sess.ID).Error)
	require.Equal(t, node.SessionStatusClosed, closed.Status)

	var closedCount int64
	require.NoError(t, home.DB.WithContext(t.Context()).Model(&node.Session{}).
		Where("subject = ? AND status = ?", channel.String(), node.SessionStatusClosed).Count(&closedCount).Error)
	require.EqualValues(t, 1, closedCount)
	require.Equal(t, block/4, loadSettled(t, home, channel))

	meter, ok := admitted.Enforcer().(*session.Meter)
	require.True(t, ok)
	require.Equal(t, 2*block-block/4, meter.Headroom(session.Usage{}))
}

func TestRuntime_AuthorizeParty_LeaseUsesProviderScopedGrant(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	provider, grantee, stranger := solana.PublicKey{41}, solana.PublicKey{42}, solana.PublicKey{43}
	position := &node.Position{
		Address:  solana.PublicKey{40}.String(),
		Kind:     node.KindLease,
		Owner:    home.OwnerAddress.String(),
		Provider: provider.String(),
	}
	require.NoError(t, home.RPC.SetGrant(
		home.ProgramClient.ProgramID, home.OwnerAddress, grantee, provider, dvpn.PermOpenLease,
	))
	require.NoError(t, home.RPC.SetGrant(
		home.ProgramClient.ProgramID,
		home.OwnerAddress,
		stranger,
		provider,
		dvpn.PermOpenChannel,
	))

	require.NoError(t, node.AuthorizeParty(t.Context(), r, position, home.OwnerAddress))
	require.NoError(t, node.AuthorizeParty(t.Context(), r, position, grantee))
	require.ErrorIs(t, node.AuthorizeParty(t.Context(), r, position, stranger), api.ErrUnauthorized)
}

func TestRuntime_LeaseHealthy_UsesFrozenRate(t *testing.T) {
	t.Parallel()

	r, _, _ := newRuntime(t)
	_, chainNode := node.ChainState(r)
	now := time.Now().Unix()
	frozen := chainNode.LeasePricePerHour
	require.NotZero(t, frozen)

	elapsedHours := uint64(24)
	owedElapsed := session.Owed(session.BasisPerHour, frozen, elapsedHours*3600)
	threshold := owedElapsed + 1
	require.NotZero(t, threshold)
	position := &node.Position{
		Cumulative:     threshold,
		Escrow:         threshold + 1,
		Kind:           node.KindLease,
		Mode:           dvpn.PositionModeVoucher,
		NodeOpenedUnix: chainNode.OpenedAt,
		OpenedUnix:     now - int64(elapsedHours*3600),
		RatePerHour:    frozen,
		Status:         node.PositionStatusOpen,
	}
	require.True(t, node.LeaseHealthy(r, position, chainNode, now))
	chainNode.LeasePricePerHour = frozen * 10
	require.True(t, node.LeaseHealthy(r, position, chainNode, now))
	position.Cumulative = owedElapsed
	require.False(t, node.LeaseHealthy(r, position, chainNode, now))
}
