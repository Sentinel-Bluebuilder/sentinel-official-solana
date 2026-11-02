package client_test

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/client"
	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestRuntime_Recover_RevertsLeftoverDNS(t *testing.T) {
	t.Parallel()

	fixture := newKillSwitchFixture(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fixture.startAndSettle(t, ctx)

	dns := &recordingDNSBackend{}
	_, _ = restartRuntime(t, ctx, fixture.home, fixture.programClient, fixture.firewall, fixture.ks.lookup, dns)

	require.Equal(t, []string{"fake0"}, dns.reverts())

	rows := firewallRows(t, fixture.home)
	require.Len(t, rows, 1)
	require.False(t, rows[0].DNSApplied)
}

func TestRuntime_Sample_AdvancesSpentPerHour(t *testing.T) {
	t.Parallel()

	rt, home, _ := newWorkingRuntime(t)
	client.SetTiming(rt, 50*time.Millisecond, time.Hour, 0, time.Second)

	home.Loaded.Client.Basis = session.BasisPerHour.String()
	seedHourlyPrice(t, home, 3_600)
	req := seedHandshakeChannel(t, home)
	address := req.GetCredential().GetChannel()
	require.NoError(t, home.DB.WithContext(t.Context()).Create(&client.Session{
		ChannelAddress: address,
		ID:             "previous",
		PaidBaseUnits:  100,
		Status:         client.SessionStatusEnded,
	}).Error)
	home.NodeService.SeedLedger(home.DaemonKey, 100, 100)

	started, err := client.StartSession(t.Context(), rt, req)
	require.NoError(t, err)
	require.Equal(t, uint64(100), started.SettledBaseUnits)
	require.Eventually(t, func() bool {
		var stored client.Session
		if home.DB.WithContext(t.Context()).First(&stored, "id = ?", started.ID).Error != nil {
			return false
		}

		return stored.SpentBaseUnits > 0
	}, 3*time.Second, 10*time.Millisecond)
	require.Zero(t, home.NodeService.VoucherCount())
}

func TestRuntime_StartSession_BoundsOpeningSettlement(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		settled uint64
		wantErr error
	}{
		{name: "stale signed exposure settles", settled: 2_000},
		{name: "new opening voucher cannot settle", settled: 2_001, wantErr: session.ErrSettledExceedsBound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rt, home, state := newWorkingRuntime(t)
			home.NodeService.SeedLedger(home.DaemonKey, 2_000, 0)
			home.NodeService.SetUsage(session.Usage{RxBytes: 390_000_000})

			changeSettlement := func() {
				home.NodeService.SetUsage(session.Usage{})
				home.NodeService.SetSettled(tc.settled)
			}
			client.SetService(rt, sdktest.FakeName, fakeClientConfig{
				hooks: clientHooks{onPeerRequest: &changeSettlement},
				state: state,
			})

			sess, err := client.StartSession(t.Context(), rt, newChannelRequest(home.NodeAddress.String()))

			require.ErrorIs(t, err, tc.wantErr)
			require.Equal(t, 1, home.NodeService.ChannelReadCount())
			require.Equal(t, 1, home.NodeService.OpenedCount())
			require.Equal(t, uint64(2_450), home.NodeService.OpenedRequest(0).Voucher.Cumulative)

			if tc.wantErr != nil {
				require.False(t, state.Started())
				require.Equal(t, 1, home.NodeService.ClosedCount())

				return
			}

			require.Equal(t, tc.settled, sess.SettledBaseUnits)
			require.NoError(t, client.StopSession(t.Context(), rt))
		})
	}
}

func TestRuntime_StartSession_DerivesHeartbeatFromMetadata(t *testing.T) {
	t.Parallel()

	rt, home, _ := newWorkingRuntime(t)
	response, err := home.NodeService.GetMetadata(t.Context(), nodeapi.GetMetadataRequestObject{})
	require.NoError(t, err)

	signed := auth.SignedMessage(response.(nodeapi.GetMetadata200JSONResponse))

	var metadata nodeapi.Metadata
	require.NoError(t, signed.Decode(&metadata))
	metadata.Settlement.HeartbeatSeconds = 60
	home.NodeService.SetMetadata(metadata)

	_, err = client.StartSession(t.Context(), rt, newChannelRequest(home.NodeAddress.String()))

	require.NoError(t, err)
	require.Equal(t, time.Minute, client.ConnectionHeartbeat(rt))
}

func TestRuntime_StartSession_FailedTicketLeavesNoTunnel(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	home := testutil.NewClientHome(t, fakeRPC)
	rt := newRuntime(t, home, home.ProgramClient)

	firewall := &recordingFirewallBackend{}
	dns := &recordingDNSBackend{}

	client.SetFirewallBackend(rt, firewall)
	client.SetDNSBackend(rt, dns)

	registerFakeClientOnce()

	state := sdktest.NewFakeState(sdktest.FakeName)
	client.SetService(rt, sdktest.FakeName, fakeClientConfig{state: state})

	req := seedTicketRequest(t, fakeRPC, home, home.DaemonKey.PublicKey(), time.Now().Unix())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.StartSession(ctx, rt, req)
	require.ErrorIs(t, err, auth.ErrTicketExpired)

	require.False(t, state.Started())
	require.Empty(t, firewall.applies())
	require.Zero(t, firewall.flushes())
	require.Empty(t, dns.applies())
	require.Equal(t, 1, home.NodeService.ClosedCount())

	var tickets int64
	require.NoError(t, home.DB.WithContext(t.Context()).Model(&client.Ticket{}).Count(&tickets).Error)
	require.Zero(t, tickets)

	_, active := client.Current(rt)
	require.False(t, active)
}

func TestRuntime_StartSession_OpensChannelSessionAndStops(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	home := testutil.NewClientHome(t, fakeRPC)
	owner := solana.PublicKey{99}
	home.Loaded.Client.Owner = owner.String()
	home.OwnerAddress = owner
	rt := newRuntime(t, home, installOpenChannelReactor(t, fakeRPC, home))

	firewall := &recordingFirewallBackend{}
	dns := &recordingDNSBackend{}

	client.SetFirewallBackend(rt, firewall)
	client.SetDNSBackend(rt, dns)

	registerFakeClientOnce()

	state := sdktest.NewFakeState(sdktest.FakeName)
	client.SetService(rt, sdktest.FakeName, fakeClientConfig{state: state})

	req := newChannelRequest(home.NodeAddress.String())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sess, err := client.StartSession(ctx, rt, req)
	require.NoError(t, err)
	require.Equal(t, client.SessionStatusActive, sess.Status)

	require.Equal(t, 1, home.NodeService.OpenedCount())

	opened := home.NodeService.OpenedRequest(0)
	require.NotNil(t, opened.Voucher)
	require.Equal(t, uint64(500), opened.Voucher.Cumulative)
	require.NotNil(t, opened.Terms)
	require.Equal(t, uint64(5_000), opened.Terms.Price)
	require.Equal(t, 1, home.NodeService.ChannelReadCount())
	require.Zero(t, sess.SpentBaseUnits)
	require.Len(t, fakeRPC.Sent(), 1)

	openTx := fakeRPC.Sent()[0]
	openMetas, err := openTx.Message.Instructions[0].ResolveInstructionAccounts(&openTx.Message)
	require.NoError(t, err)
	require.Len(t, openMetas, 13)
	require.True(t, openMetas[11].IsSigner)
	require.Equal(t, openMetas[0].PublicKey, openMetas[11].PublicKey)
	require.Equal(
		t,
		[]solana.PublicKey{home.DaemonKey.PublicKey(), home.DaemonKey.PublicKey()},
		testutil.ParseOpenRentRecipients(t, openTx.Message.Instructions[0].Data),
	)

	fwApplied := firewall.applies()
	require.Len(t, fwApplied, 1)
	require.Equal(t, "fake0", fwApplied[0].iface)

	var fwRow client.Firewall
	require.NoError(t, home.DB.WithContext(t.Context()).First(&fwRow).Error)

	dnsApplied := dns.applies()
	require.Len(t, dnsApplied, 1)
	require.Equal(t, "fake0", dnsApplied[0].iface)

	require.Equal(t, "127.0.0.1", state.Host())
	require.True(t, state.Started())
	require.False(t, state.Stopped())

	current, active := client.Current(rt)
	require.True(t, active)
	require.Equal(t, sess.ID, current.ID)

	_, err = client.StartSession(ctx, rt, req)
	require.ErrorIs(t, err, client.ErrSessionActive)

	state.SetLocalStats(service.PeerStats{RxBytes: 30_000_000})
	require.NoError(t, client.SampleAndIssue(ctx, rt))
	require.NoError(t, client.StopSession(ctx, rt))

	require.Equal(t, 1, home.NodeService.ClosedCount())
	require.Equal(t, sess.ID, home.NodeService.ClosedSessionID(0))
	require.True(t, state.Stopped())
	require.Len(t, dns.reverts(), 1)
	require.Equal(t, 1, firewall.flushes())

	var count int64
	require.NoError(t, home.DB.WithContext(t.Context()).Model(&client.Firewall{}).Count(&count).Error)
	require.Zero(t, count)

	var ended client.Session
	require.NoError(t, home.DB.WithContext(t.Context()).First(&ended, "id = ?", sess.ID).Error)
	require.Equal(t, client.SessionStatusEnded, ended.Status)
	require.True(t, ended.Clean)
	require.Equal(t, uint64(150), ended.OwedBaseUnits)
	require.Equal(t, uint64(500), ended.PaidBaseUnits)
	require.Zero(t, ended.SettledBaseUnits)

	_, active = client.Current(rt)
	require.False(t, active)
}

func TestRuntime_StartSession_RecordsOpeningBeforeNetworkAndFailedSetup(t *testing.T) {
	t.Parallel()

	rt, home, _ := newWorkingRuntime(t)
	client.SetTiming(rt, time.Hour, time.Hour, 0, time.Second)

	home.Loaded.Client.Basis = session.BasisPerHour.String()
	seedHourlyPrice(t, home, 3_600)
	req := seedHandshakeChannel(t, home)
	channelAddress := req.GetCredential().GetChannel()

	var recorded atomic.Bool

	handler := home.NodeServer.Config.Handler
	home.NodeServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/sessions" {
			var (
				channel client.Channel
				rows    []client.Session
			)
			if home.DB.First(&channel, "address = ?", channelAddress).Error == nil &&
				home.DB.Where("channel_address = ?", channelAddress).Find(&rows).Error == nil && len(rows) == 1 &&
				rows[0].PaidBaseUnits == channel.Cumulative && channel.Cumulative > 0 {
				recorded.Store(true)
			}
		}

		handler.ServeHTTP(w, r)
	})
	dns := &recordingDNSBackend{failFrom: 1}
	client.SetDNSBackend(rt, dns)

	_, err := client.StartSession(t.Context(), rt, req)
	require.ErrorIs(t, err, errDNSApplyFailed)
	require.True(t, recorded.Load())

	var rows []client.Session
	require.NoError(t, home.DB.Where("channel_address = ?", channelAddress).Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, client.SessionStatusEnded, rows[0].Status)
	require.False(t, rows[0].Clean)
	require.Equal(t, uint64(60), rows[0].PaidBaseUnits)
	require.Zero(t, rows[0].SettledBaseUnits)

	home.NodeService.SetSettled(20)
	client.SetDNSBackend(rt, &recordingDNSBackend{})
	client.SetTiming(rt, 50*time.Millisecond, time.Hour, 0, time.Second)
	started, err := client.StartSession(t.Context(), rt, req)
	require.NoError(t, err)
	require.Equal(t, uint64(20), started.SettledBaseUnits)

	var sampled client.Session

	require.Eventually(t, func() bool {
		require.NoError(t, home.DB.WithContext(t.Context()).First(&sampled, "id = ?", started.ID).Error)

		return sampled.Seconds >= 1
	}, 3*time.Second, 10*time.Millisecond)
	require.GreaterOrEqual(t, sampled.SpentBaseUnits, sampled.Seconds)
	require.Equal(t, uint64(20), sampled.SettledBaseUnits)
	bound, err := client.SettledBound(t.Context(), rt, channelAddress, 60, 5)
	require.NoError(t, err)
	require.Equal(t, uint64(60), bound)
	require.NoError(t, client.StopSession(t.Context(), rt))
}

func TestRuntime_StartSession_RefusesEscrowBelowMinimum(t *testing.T) {
	t.Parallel()

	rpc := sdktest.NewFakeRPC(t)
	home := testutil.NewClientHome(t, rpc)
	home.Loaded.Client.EscrowBaseUnits = 200
	home.NodeService.SetEscrow(200)
	rt := newRuntime(t, home, installOpenChannelReactor(t, rpc, home))

	_, err := client.StartSession(t.Context(), rt, newChannelRequest(home.NodeAddress.String()))

	require.ErrorContains(t, err, "fund or close the channel")
	require.Zero(t, home.NodeService.OpenedCount())
	require.Empty(t, rpc.Sent())

	var channels int64
	require.NoError(t, home.DB.WithContext(t.Context()).Model(&client.Channel{}).Count(&channels).Error)
	require.Zero(t, channels)
}

func TestRuntime_StartSession_RefusesMinimumAboveMaximum(t *testing.T) {
	t.Parallel()

	rt, home, _ := newWorkingRuntime(t)
	node, err := home.ProgramClient.FetchNode(t.Context(), home.NodeAddress)
	require.NoError(t, err)
	metadata, _, err := nodeapi.Discover(t.Context(), home.ProgramClient, home.NodeAddress, node.Value, time.Second)
	require.NoError(t, err)

	metadata.Settlement.MinChannelPrepayBytes = 3_000_000_000
	home.NodeService.SetMetadata(metadata)

	_, err = client.StartSession(t.Context(), rt, newChannelRequest(home.NodeAddress.String()))

	require.ErrorIs(t, err, session.ErrPrepayExceedsMaximum)
	require.Zero(t, home.NodeService.ChannelReadCount())
}

func TestRuntime_StartSession_RejectsForeignLedger(t *testing.T) {
	t.Parallel()

	rt, home, _ := newWorkingRuntime(t)
	otherKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	home.NodeService.SeedLedger(otherKey, 2_000, 0)

	_, err = client.StartSession(t.Context(), rt, newChannelRequest(home.NodeAddress.String()))

	require.ErrorIs(t, err, session.ErrInvalidLedger)
	require.Zero(t, home.NodeService.OpenedCount())
}

func TestRuntime_StartSession_RejectsInflatedSettled(t *testing.T) {
	t.Parallel()

	rt, home, _ := newWorkingRuntime(t)
	home.NodeService.SeedLedger(home.DaemonKey, 2_000, 2_000)

	_, err := client.StartSession(t.Context(), rt, newChannelRequest(home.NodeAddress.String()))

	require.ErrorIs(t, err, session.ErrSettledExceedsBound)
	require.Zero(t, home.NodeService.OpenedCount())
}

func TestRuntime_StartSession_RejectsPrepayBelowMinimum(t *testing.T) {
	t.Parallel()

	rt, home, _ := newWorkingRuntime(t)
	home.NodeService.SetOpenRejections(1)

	_, err := client.StartSession(t.Context(), rt, newChannelRequest(home.NodeAddress.String()))

	require.ErrorIs(t, err, api.ErrPrepayBelowMinimum)
	require.Equal(t, 1, home.NodeService.OpenedCount())
}

func TestRuntime_StartSession_RejectsTicketOfOtherOwner(t *testing.T) {
	t.Parallel()

	fixture := newTicketFixture(t)
	device := fixture.home.DaemonKey.PublicKey()
	req := seedTicketRequest(t, fixture.rpc, fixture.home, device, time.Now().Add(time.Hour).Unix())

	tickets := ticketCount(t, fixture.home)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	_, err := client.StartSession(ctx, fixture.rt, req)
	require.ErrorIs(t, err, client.ErrTicketOwnerMismatch)
	require.False(t, fixture.state.Started())
	require.Empty(t, fixture.firewall.applies())
	require.Equal(t, 1, fixture.home.NodeService.ClosedCount())
	require.Equal(t, tickets, ticketCount(t, fixture.home))
	_, active := client.Current(fixture.rt)
	require.False(t, active)
	require.ErrorContains(t, err, device.String())
	require.ErrorContains(t, err, fixture.owner.String())
}

func TestRuntime_StartSession_ResumesNodeLedger(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		localPaid uint64
		name      string
		wantPaid  uint64
	}{
		{name: "node surplus reused", wantPaid: 2_000},
		{localPaid: 2_500, name: "highest local voucher retained", wantPaid: 2_500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rt, home, _ := newWorkingRuntime(t)
			req := seedHandshakeChannel(t, home)
			channel := client.Channel{
				Address:    req.GetCredential().GetChannel(),
				Cumulative: tc.localPaid,
			}
			require.NoError(t, home.DB.WithContext(t.Context()).Create(&channel).Error)
			ended := client.Session{
				ChannelAddress: channel.Address,
				ID:             "unclean",
				PaidBaseUnits:  tc.wantPaid,
				Status:         client.SessionStatusEnded,
			}
			require.NoError(t, home.DB.WithContext(t.Context()).Create(&ended).Error)
			home.NodeService.SeedLedger(home.DaemonKey, 2_000, 1_500)
			home.NodeService.SetSettled(1_500)

			sess, err := client.StartSession(t.Context(), rt, req)

			require.NoError(t, err)
			require.Equal(t, 1, home.NodeService.OpenedCount())
			require.Equal(t, tc.wantPaid, home.NodeService.OpenedRequest(0).Voucher.Cumulative)
			require.Equal(t, uint64(1_500), sess.SettledBaseUnits)
			require.Equal(t, tc.wantPaid, sess.PaidBaseUnits)
			require.NoError(t, client.StopSession(t.Context(), rt))
		})
	}
}

func TestRuntime_StartSession_StreamIgnoresVoucherPrepayment(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	home := testutil.NewClientHome(t, fakeRPC)
	rt := newRuntime(t, home, home.ProgramClient)
	node, err := home.ProgramClient.FetchNode(t.Context(), home.NodeAddress)
	require.NoError(t, err)
	metadata, _, err := nodeapi.Discover(t.Context(), home.ProgramClient, home.NodeAddress, node.Value, time.Second)
	require.NoError(t, err)

	metadata.Settlement.MinChannelPrepayBytes = 3_000_000_000
	home.NodeService.SetMetadata(metadata)
	req := newChannelRequest(home.NodeAddress.String())
	req.GetCredential().GetNewChannel().Mode = uint32(dvpn.PositionModeStream)

	_, err = client.StartSession(t.Context(), rt, req)
	require.Error(t, err)
	require.NotErrorIs(t, err, session.ErrPrepayExceedsMaximum)
	require.Len(t, fakeRPC.Sent(), 1)

	var channels []client.Channel
	require.NoError(t, home.DB.WithContext(t.Context()).Find(&channels).Error)
	require.Len(t, channels, 1)
	require.Equal(t, client.ChannelStatusPending, channels[0].Status)
}

func TestRuntime_StopSession_PreservesFinalAccounting(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{
		"confirmed close",
		"failed close",
		"failed sample",
		"closing response",
		"closed event",
	} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()

			rt, home, state := newWorkingRuntime(t)
			client.SetTiming(rt, time.Hour, time.Hour, 0, time.Second)

			req := seedHandshakeChannel(t, home)

			var failClose atomic.Bool

			handler := home.NodeServer.Config.Handler
			home.NodeServer.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete && failClose.Load() {
					w.WriteHeader(http.StatusServiceUnavailable)

					return
				}

				handler.ServeHTTP(w, r)
			})

			var failStats atomic.Bool

			statsError := func() error {
				if failStats.Load() {
					return errors.New("stats unavailable")
				}

				return nil
			}
			client.SetService(rt, sdktest.FakeName, fakeClientConfig{
				hooks: clientHooks{onStatsError: &statsError}, state: state,
			})
			started, err := client.StartSession(t.Context(), rt, req)
			require.NoError(t, err)
			state.SetLocalStats(service.PeerStats{RxBytes: 20_000_000})
			failClose.Store(scenario == "failed close" || scenario == "closing response" || scenario == "closed event")
			failStats.Store(scenario == "failed sample")

			if scenario == "closing response" {
				setNodeResponse(t, rt, http.StatusOK, nodeapi.Session{
					CloseReason: nodeapi.ClosedReasonLeaseNotFunded,
					Ledger:      nodeapi.Ledger{Basis: session.BasisPerGB},
					Status:      nodeapi.SessionStatusClosing,
				})
				require.EqualError(t, client.Poll(t.Context(), rt), "remote session is not active: lease not funded")
			}

			if scenario == "closed event" {
				require.Eventually(t, func() bool {
					return home.NodeService.StreamOpenCount() >= 1
				}, time.Second, time.Millisecond)
				home.NodeService.PublishClosed(nodeapi.ReasonLeaseNotFunded)
				require.Eventually(t, func() bool {
					var row client.Session
					require.NoError(t, home.DB.First(&row, "id = ?", started.ID).Error)

					return row.Status == client.SessionStatusEnded
				}, 5*time.Second, 5*time.Millisecond)
			} else {
				require.NoError(t, client.StopSession(t.Context(), rt))
			}

			var ended client.Session
			require.NoError(t, home.DB.First(&ended, "id = ?", started.ID).Error)
			require.Equal(t, scenario == "confirmed close", ended.Clean)

			settled := uint64(100)

			if ended.Clean {
				require.Equal(t, uint64(20_000_000), ended.RxBytes)
				require.Equal(t, settled, ended.OwedBaseUnits)
			} else {
				settled = ended.PaidBaseUnits
				bound, boundErr := client.SettledBound(t.Context(), rt, req.GetCredential().GetChannel(), 500, 5)
				require.NoError(t, boundErr)
				require.Equal(t, ended.PaidBaseUnits, bound)
			}

			home.NodeService.SetUsage(session.Usage{})
			home.NodeService.SetSettled(settled)
			failClose.Store(false)
			failStats.Store(false)

			_, err = client.StartSession(t.Context(), rt, req)
			require.NoError(t, err)
			require.NoError(t, client.StopSession(t.Context(), rt))
		})
	}
}

func seedHandshakeChannel(t *testing.T, home testutil.Home) *daemonv1.StartSessionRequest {
	t.Helper()

	key, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	channel := dvpn.Channel{
		Escrow:        home.Loaded.Client.EscrowBaseUnits,
		Mode:          dvpn.PositionModeVoucher,
		Node:          home.NodeAddress,
		Owner:         home.DaemonKey.PublicKey(),
		VoucherSigner: home.DaemonKey.PublicKey(),
	}
	home.RPC.SetAccount(key.PublicKey(), sdktest.Account{
		Data:  sdktest.EncodeAccount(dvpn.ChannelAccountDiscriminator, channel),
		Owner: home.ProgramClient.ProgramID,
	})

	return &daemonv1.StartSessionRequest{
		Credential: &daemonv1.Credential{Kind: &daemonv1.Credential_Channel{Channel: key.PublicKey().String()}},
		Node:       home.NodeAddress.String(),
	}
}

func seedHourlyPrice(t *testing.T, home testutil.Home, pricePerHour uint64) {
	t.Helper()

	node, err := home.ProgramClient.FetchNode(t.Context(), home.NodeAddress)
	require.NoError(t, err)
	metadata, _, err := nodeapi.Discover(t.Context(), home.ProgramClient, home.NodeAddress, node.Value, time.Second)
	require.NoError(t, err)

	node.Value.ChannelPricePerGB = 0
	node.Value.ChannelPricePerHour = pricePerHour
	home.RPC.SetNode(home.ProgramClient.ProgramID, home.NodeAddress, node.Value)

	metadata.Settlement.Prices.ChannelPricePerGB = 0
	metadata.Settlement.Prices.ChannelPricePerHour = pricePerHour
	home.NodeService.SetMetadata(metadata)
}
