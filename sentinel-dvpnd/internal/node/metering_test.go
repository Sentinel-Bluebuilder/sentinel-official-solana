//go:build linux

package node_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/sentinel-official/sentinel-dvpnd/internal/node"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

type failingStatsServer struct {
	service.Server
}

func (s failingStatsServer) PeerStats(context.Context, service.PeerID) (service.PeerStats, error) {
	return service.PeerStats{}, errors.New("stats unavailable")
}

func TestRuntime_AcceptVoucher_RaisesLiveMeter(t *testing.T) {
	t.Parallel()

	r, home, state := newRuntime(t)
	channel := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
	terms := session.Terms{Basis: session.BasisPerGB, Price: 1_000_000_000}
	_, chainNode := node.ChainState(r)
	chainNode.ChannelPricePerGB = terms.Price
	node.SetChainNode(r, chainNode)
	admitted, err := node.Admit(t.Context(), r, home.OwnerAddress,
		voucherRequest(t, home, channel, terms, 100_000_000))
	require.NoError(t, err)
	sess, _, err := node.OpenSession(t.Context(), r, admitted, peerRequestFixture())
	require.NoError(t, err)

	frames, unsubscribe := node.Subscribe(r, sess.ID)
	t.Cleanup(unsubscribe)
	state.SetStats(service.PeerID(sess.PeerID), service.PeerStats{RxBytes: 99_999_999})
	require.NoError(t, node.SyncUsage(t.Context(), r))
	decodeMeteringEvent(t, frames, nodeapi.EventVoucherRequest)

	req := voucherRequest(t, home, channel, terms, 200_000_000)
	_, err = node.AcceptChannelVoucher(t.Context(), r, channel, home.OwnerAddress, *req.Voucher)
	require.NoError(t, err)

	paid, ok := node.MeterPaid(r, sess.ID)
	require.True(t, ok)
	require.EqualValues(t, 200_000_000, paid)
	state.SetStats(service.PeerID(sess.PeerID), service.PeerStats{RxBytes: 100_000_001})
	require.NoError(t, node.SyncUsage(t.Context(), r))
	require.Empty(t, frames)
	require.NoError(t, home.DB.WithContext(t.Context()).First(sess, "id = ?", sess.ID).Error)
	require.Equal(t, node.SessionStatusActive, sess.Status)

	state.SetStats(service.PeerID(sess.PeerID), service.PeerStats{RxBytes: 200_000_000})
	require.NoError(t, node.SyncUsage(t.Context(), r))
	closed := decodeMeteringEvent(t, frames, nodeapi.EventClosed)

	var event nodeapi.ClosedEvent
	require.NoError(t, json.Unmarshal(closed.Data, &event))
	require.Equal(t, nodeapi.ClosedReasonCreditExhausted, event.Reason)
}

func TestRuntime_RefreshLeaseHealth_FlipsTicketEnforcer(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	now := time.Now().Unix()
	_, chainNode := node.ChainState(r)
	position := node.Position{
		Address:        home.OwnerKey.PublicKey().String(),
		Cumulative:     999,
		Kind:           node.KindLease,
		Mode:           dvpn.PositionModeVoucher,
		NodeOpenedUnix: chainNode.OpenedAt,
		OpenedUnix:     now - 1,
		RatePerHour:    3_600_000,
		Status:         node.PositionStatusOpen,
	}
	require.NoError(t, home.DB.WithContext(t.Context()).Create(&position).Error)
	sess := seedSession(t, home, "session-ticket", "peer-ticket")
	sess.Credential = node.CredentialTicket
	require.NoError(t, home.DB.WithContext(t.Context()).Save(sess).Error)
	ticket := node.Ticket{Hash: sess.Subject, Lease: position.Address, LeaseHealthy: true, Session: sess.ID}
	require.NoError(t, home.DB.WithContext(t.Context()).Create(&ticket).Error)

	enforcer := session.NewTicketEnforcer(0, now+3_600)
	node.SetEnforcer(r, sess.ID, enforcer)

	for _, paid := range []uint64{999, 1_001, 1_000, 1_001} {
		position.Cumulative = paid
		require.NoError(t, home.DB.WithContext(t.Context()).Save(&position).Error)
		node.RefreshLeaseHealth(t.Context(), r, now)
		require.NoError(t, home.DB.WithContext(t.Context()).First(&ticket, "hash = ?", ticket.Hash).Error)
		require.Equal(t, paid > 1_000, ticket.LeaseHealthy)
		require.Equal(t, paid <= 1_000, enforcer.Exhausted(session.Usage{}, now))
	}

	position.Cumulative = 0
	require.NoError(t, home.DB.WithContext(t.Context()).Save(&position).Error)
	require.NoError(t, node.SyncUsage(t.Context(), r))
	require.True(t, enforcer.Exhausted(session.Usage{}, now))
	require.NoError(t, home.DB.WithContext(t.Context()).First(sess, "id = ?", sess.ID).Error)
	require.Equal(t, node.SessionStatusClosed, sess.Status)
}

func TestRuntime_RequestLeaseVouchers_AtHalfMinimum(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	r, home, _ := newRuntimeWithRegistry(t, reg)
	now := time.Now().Unix()
	_, chainNode := node.ChainState(r)
	position := node.Position{
		Address:        home.OwnerKey.PublicKey().String(),
		Cumulative:     3_600_000,
		Kind:           node.KindLease,
		Mode:           dvpn.PositionModeVoucher,
		NodeOpenedUnix: chainNode.OpenedAt,
		OpenedUnix:     now - 1_799,
		RatePerHour:    3_600_000,
		Status:         node.PositionStatusOpen,
	}
	require.NoError(t, home.DB.WithContext(t.Context()).Create(&position).Error)
	frames, unsubscribe := node.Subscribe(r, position.Address)
	t.Cleanup(unsubscribe)

	node.RequestLeaseVouchers(t.Context(), r, now)
	require.Empty(t, frames)

	position.OpenedUnix = now - 1_800
	require.NoError(t, home.DB.WithContext(t.Context()).Save(&position).Error)
	node.RequestLeaseVouchers(t.Context(), r, now)
	frame := decodeMeteringEvent(t, frames, nodeapi.EventVoucherRequest)

	var event nodeapi.VoucherRequestEvent
	require.NoError(t, json.Unmarshal(frame.Data, &event))
	require.GreaterOrEqual(t, event.Consumption.OwedBaseUnits, uint64(1_800_000))
	require.EqualValues(t, 3_600_000, event.Ledger.CumulativeBaseUnits)
	require.Empty(t, frames)
	require.NoError(t, node.SyncUsage(t.Context(), r))
	require.Empty(t, frames)
	require.InDelta(t, 1, voucherRequestCount(t, reg, node.KindLease), 0)

	node.SetVoucherRequestSentUnix(
		r, position.Address, time.Now().Unix()-int64(home.Loaded.Node.Sessions.HeartbeatSeconds)-1,
	)
	node.RequestLeaseVouchers(t.Context(), r, now)
	decodeMeteringEvent(t, frames, nodeapi.EventVoucherRequest)
	require.Empty(t, frames)
	require.InDelta(t, 2, voucherRequestCount(t, reg, node.KindLease), 0)

	position.Cumulative--
	require.NoError(t, home.DB.WithContext(t.Context()).Save(&position).Error)
	node.RequestLeaseVouchers(t.Context(), r, now)
	decodeMeteringEvent(t, frames, nodeapi.EventVoucherRequest)
	require.Empty(t, frames)
	require.InDelta(t, 3, voucherRequestCount(t, reg, node.KindLease), 0)
}

func TestRuntime_SyncUsage_AdvancesCounters(t *testing.T) {
	t.Parallel()

	r, home, state := newRuntime(t)
	sess := seedSession(t, home, "session-usage", "peer-usage")
	node.SetEnforcer(r, sess.ID, session.NewMeter(session.Terms{Basis: session.BasisPerGB, Price: 1}, 1_000, 0))
	state.SetStats(service.PeerID(sess.PeerID), service.PeerStats{RxBytes: 1_000, TxBytes: 500})

	before := time.Now().Unix()

	require.NoError(t, node.SyncUsage(t.Context(), r))

	var got node.Session
	require.NoError(t, home.DB.WithContext(t.Context()).First(&got, "id = ?", sess.ID).Error)
	require.EqualValues(t, 1_000, got.RxBytes)
	require.EqualValues(t, 500, got.TxBytes)
	require.EqualValues(t, 1_000, got.RxCounter)
	require.EqualValues(t, 500, got.TxCounter)
	require.GreaterOrEqual(t, got.LastTrafficUnix, before)
	require.LessOrEqual(t, got.LastTrafficUnix, time.Now().Unix())
	state.SetStats(service.PeerID(sess.PeerID), service.PeerStats{RxBytes: 200, TxBytes: 100})
	require.NoError(t, node.SyncUsage(t.Context(), r))
	require.NoError(t, home.DB.WithContext(t.Context()).First(&got, "id = ?", sess.ID).Error)
	require.EqualValues(t, 1_200, got.RxBytes)
	require.EqualValues(t, 600, got.TxBytes)
	require.EqualValues(t, 200, got.RxCounter)
	require.EqualValues(t, 100, got.TxCounter)
}

func TestRuntime_SyncUsage_ClosesExhaustedAndIdle(t *testing.T) {
	t.Parallel()

	r, home, state := newRuntime(t)
	exhausted := seedSession(t, home, "session-exhausted", "peer-exhausted")
	idle := seedSession(t, home, "session-idle", "peer-idle")
	closing := seedSession(t, home, "session-closing", "peer-closing")
	closing.Status = node.SessionStatusClosing
	require.NoError(t, home.DB.WithContext(t.Context()).Save(closing).Error)
	idle.LastTrafficUnix = time.Now().Unix() - int64(home.Loaded.Node.Sessions.IdleSeconds) - 1
	require.NoError(t, home.DB.WithContext(t.Context()).Save(idle).Error)

	terms := session.Terms{Basis: session.BasisPerGB, Price: 1_000_000_000}
	exhausted.Basis = uint8(terms.Basis)
	exhausted.Price = terms.Price
	require.NoError(t, home.DB.WithContext(t.Context()).Save(exhausted).Error)
	state.SetStats(service.PeerID(exhausted.PeerID), service.PeerStats{RxBytes: 100_000_000})
	node.SetEnforcer(r, exhausted.ID, session.NewMeter(terms, 100_000_000, 0))
	node.SetEnforcer(r, idle.ID, session.NewMeter(session.Terms{Basis: session.BasisPerGB, Price: 1}, 1, 0))
	frames, unsubscribe := node.Subscribe(r, exhausted.ID)
	t.Cleanup(unsubscribe)
	require.NoError(t, node.SyncUsage(t.Context(), r))

	var gotExhausted node.Session
	require.NoError(t, home.DB.WithContext(t.Context()).First(&gotExhausted, "id = ?", exhausted.ID).Error)
	require.Equal(t, node.SessionStatusClosed, gotExhausted.Status)
	closed := decodeMeteringEvent(t, frames, nodeapi.EventClosed)

	var event nodeapi.ClosedEvent
	require.NoError(t, json.Unmarshal(closed.Data, &event))
	require.Equal(t, nodeapi.ClosedReasonCreditExhausted, event.Reason)

	var gotIdle node.Session
	require.NoError(t, home.DB.WithContext(t.Context()).First(&gotIdle, "id = ?", idle.ID).Error)
	require.Equal(t, node.SessionStatusClosed, gotIdle.Status)

	require.NoError(t, home.DB.WithContext(t.Context()).First(closing, "id = ?", closing.ID).Error)
	require.Equal(t, node.SessionStatusClosed, closing.Status)
	wantRemoved := []service.PeerID{
		service.PeerID(exhausted.PeerID), service.PeerID(idle.PeerID), service.PeerID(closing.PeerID),
	}
	require.ElementsMatch(t, wantRemoved, state.RemovedPeers())

	for _, job := range r.Jobs() {
		require.NotEqual(t, "enforce_credit", job.Name)

		if job.Name == "sync_usage" {
			require.Equal(t, time.Second, job.Interval)
		}
	}
}

func TestRuntime_SyncUsage_ContinuesAfterSessionWriteFailure(t *testing.T) {
	t.Parallel()

	r, home, state := newRuntime(t)
	first := seedSession(t, home, "session-first", "peer-first")

	second := seedSession(t, home, "session-second", "peer-second")
	for _, sess := range []*node.Session{first, second} {
		state.SetStats(service.PeerID(sess.PeerID), service.PeerStats{RxBytes: 10})
		node.SetEnforcer(r, sess.ID, session.NewMeter(session.Terms{Basis: session.BasisPerGB, Price: 1}, 0, 0))
	}

	require.NoError(t, home.DB.Callback().Update().After("gorm:update").Register("test:fail_first", func(db *gorm.DB) {
		if _, ok := db.Statement.Model.(*node.Session); ok && slices.Contains(db.Statement.Vars, any(first.ID)) {
			db.Error = errors.New("session write failed")
		}
	}))

	require.NoError(t, node.SyncUsage(t.Context(), r))
	require.NoError(t, home.DB.WithContext(t.Context()).First(second, "id = ?", second.ID).Error)
	require.Equal(t, node.SessionStatusClosed, second.Status)
}

func TestRuntime_SyncUsage_EnforcesWithoutStats(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{"hourly", "idle"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()

			r, home, state := newRuntime(t)
			node.SetService(r, failingStatsServer{Server: sdktest.NewFakeServer(state)})

			sess := seedSession(t, home, "session-stats", "peer-stats")
			sess.Basis = uint8(session.BasisPerHour)
			sess.CreatedUnix = time.Now().Unix() - 60
			sess.Price = 3_600
			sess.RxBytes = 100
			sess.RxCounter = 100
			paid := uint64(60)

			if scenario == "idle" {
				sess.LastTrafficUnix = time.Now().Unix() - int64(home.Loaded.Node.Sessions.IdleSeconds) - 1
				paid = 3_600
			}

			require.NoError(t, home.DB.WithContext(t.Context()).Save(sess).Error)
			node.SetEnforcer(r, sess.ID, session.NewMeter(session.Terms{Basis: session.BasisPerHour, Price: 3_600}, paid, 0))

			require.NoError(t, node.SyncUsage(t.Context(), r))
			require.NoError(t, home.DB.WithContext(t.Context()).First(sess, "id = ?", sess.ID).Error)
			require.Equal(t, node.SessionStatusClosed, sess.Status)
			require.EqualValues(t, 100, sess.RxBytes)
			require.EqualValues(t, 100, sess.RxCounter)
			require.GreaterOrEqual(t, sess.Seconds, uint64(60))
		})
	}
}

func TestRuntime_SyncUsage_IgnoresKeepaliveTraffic(t *testing.T) {
	t.Parallel()

	r, home, state := newRuntime(t)
	keepalive := seedSession(t, home, "session-keepalive", "peer-keepalive")
	traffic := seedSession(t, home, "session-traffic", "peer-traffic")

	lastTraffic := time.Now().Unix() - int64(home.Loaded.Node.Sessions.IdleSeconds) - 1
	for _, sess := range []*node.Session{keepalive, traffic} {
		sess.LastTrafficUnix = lastTraffic
		require.NoError(t, home.DB.WithContext(t.Context()).Save(sess).Error)
		node.SetEnforcer(r, sess.ID, session.NewMeter(session.Terms{
			Basis: session.BasisPerGB,
			Price: 1,
		}, 1_000_000, 0))
	}

	frames, unsubscribe := node.Subscribe(r, keepalive.ID)
	t.Cleanup(unsubscribe)
	state.SetStats(service.PeerID(keepalive.PeerID), service.PeerStats{
		RxBytes: 16,
		TxBytes: 16,
	})
	state.SetStats(service.PeerID(traffic.PeerID), service.PeerStats{
		RxBytes: 300,
		TxBytes: 300,
	})
	require.NoError(t, node.SyncUsage(t.Context(), r))

	require.NoError(t, home.DB.WithContext(t.Context()).First(keepalive, "id = ?", keepalive.ID).Error)
	require.Equal(t, node.SessionStatusClosed, keepalive.Status)
	require.Equal(t, nodeapi.ReasonIdle, keepalive.CloseReason)
	require.Equal(t, lastTraffic, keepalive.LastTrafficUnix)
	require.EqualValues(t, 16, keepalive.RxBytes)
	require.EqualValues(t, 16, keepalive.TxBytes)
	closed := decodeMeteringEvent(t, frames, nodeapi.EventClosed)

	var event nodeapi.ClosedEvent
	require.NoError(t, json.Unmarshal(closed.Data, &event))
	require.Equal(t, nodeapi.ClosedReasonIdle, event.Reason)

	require.NoError(t, home.DB.WithContext(t.Context()).First(traffic, "id = ?", traffic.ID).Error)
	require.Equal(t, node.SessionStatusActive, traffic.Status)
	require.Greater(t, traffic.LastTrafficUnix, lastTraffic)
	require.EqualValues(t, 300, traffic.RxBytes)
	require.EqualValues(t, 300, traffic.TxBytes)
}

func TestRuntime_SyncUsage_ReportsTicketReasons(t *testing.T) {
	t.Parallel()

	r, home, state := newRuntime(t)
	now := time.Now().Unix()
	cases := []struct {
		name     string
		enforcer *session.TicketEnforcer
		stats    service.PeerStats
		want     nodeapi.ClosedReason
	}{
		{
			name:     "expired",
			enforcer: session.NewTicketEnforcer(10, now-1),
			stats:    service.PeerStats{RxBytes: 10},
			want:     nodeapi.ClosedReasonTicketExpired,
		},
		{
			name:     "cap",
			enforcer: session.NewTicketEnforcer(10, now+1_000),
			stats:    service.PeerStats{RxBytes: 10},
			want:     nodeapi.ClosedReasonDataCapReached,
		},
		{
			name:     "lease",
			enforcer: session.NewTicketEnforcer(10, now-1),
			stats:    service.PeerStats{RxBytes: 10},
			want:     nodeapi.ClosedReasonLeaseNotFunded,
		},
	}
	cases[2].enforcer.SetLeaseHealthy(false)

	frames := make([]<-chan []byte, len(cases))
	for i, tc := range cases {
		sess := seedSession(t, home, "session-"+tc.name, "peer-"+tc.name)
		sess.Credential = node.CredentialTicket
		require.NoError(t, home.DB.WithContext(t.Context()).Save(sess).Error)
		state.SetStats(service.PeerID(sess.PeerID), tc.stats)
		node.SetEnforcer(r, sess.ID, tc.enforcer)

		var unsubscribe func()

		frames[i], unsubscribe = node.Subscribe(r, sess.ID)
		t.Cleanup(unsubscribe)
	}

	require.NoError(t, node.SyncUsage(t.Context(), r))

	for i, tc := range cases {
		closed := decodeMeteringEvent(t, frames[i], nodeapi.EventClosed)

		var event nodeapi.ClosedEvent
		require.NoError(t, json.Unmarshal(closed.Data, &event))
		require.Equal(t, tc.want, event.Reason, tc.name)
	}
}

func TestRuntime_SyncUsage_RequestsVoucherAtHalfMinimum(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	r, home, state := newRuntimeWithRegistry(t, reg)
	terms := session.Terms{Basis: session.BasisPerGB, Price: 1_000_000_000}
	channel := seedChannel(t, r, home, home.OwnerKey, dvpn.PositionModeVoucher)
	seedPosition(t, r, home, channel, 100_000_000)
	sess := seedSession(t, home, "session-request", "peer-request")
	sess.Basis = uint8(terms.Basis)
	sess.Price = terms.Price
	sess.Subject = channel.String()
	require.NoError(t, home.DB.WithContext(t.Context()).Save(sess).Error)
	node.SetEnforcer(r, sess.ID, session.NewMeter(terms, 100_000_000, 0))
	frames, unsubscribe := node.Subscribe(r, sess.ID)
	t.Cleanup(unsubscribe)
	state.SetStats(service.PeerID(sess.PeerID), service.PeerStats{RxBytes: 49_999_999})
	require.NoError(t, node.SyncUsage(t.Context(), r))
	require.Empty(t, frames)

	state.SetStats(service.PeerID(sess.PeerID), service.PeerStats{RxBytes: 50_000_000})

	require.NoError(t, node.SyncUsage(t.Context(), r))
	frame := decodeMeteringEvent(t, frames, nodeapi.EventVoucherRequest)

	var event nodeapi.VoucherRequestEvent
	require.NoError(t, json.Unmarshal(frame.Data, &event))
	require.EqualValues(t, 50_000_000, event.Consumption.OwedBaseUnits)
	require.EqualValues(t, 100_000_000, event.Ledger.CumulativeBaseUnits)
	require.Empty(t, frames)

	require.NoError(t, node.SyncUsage(t.Context(), r))
	require.Empty(t, frames)
	require.InDelta(t, 1, voucherRequestCount(t, reg, node.KindChannel), 0)

	node.SetVoucherRequestSentUnix(
		r, sess.ID, time.Now().Unix()-int64(home.Loaded.Node.Sessions.HeartbeatSeconds)-1,
	)
	require.NoError(t, node.SyncUsage(t.Context(), r))
	decodeMeteringEvent(t, frames, nodeapi.EventVoucherRequest)
	require.Empty(t, frames)
	require.InDelta(t, 2, voucherRequestCount(t, reg, node.KindChannel), 0)
}

func decodeMeteringEvent(t *testing.T, frames <-chan []byte, name string) api.Event {
	t.Helper()

	select {
	case frame := <-frames:
		event, err := api.NewEventDecoder(bytes.NewReader(frame)).Next()
		require.NoError(t, err)
		require.Equal(t, name, event.Name)

		return event
	default:
		t.Fatal("missing event", name)

		return api.Event{}
	}
}

func voucherRequestCount(t *testing.T, reg *prometheus.Registry, kind string) float64 {
	t.Helper()

	families, err := reg.Gather()
	require.NoError(t, err)

	for _, family := range families {
		if family.GetName() != "sentinel_dvpnd_voucher_requests_total" {
			continue
		}

		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "kind" && label.GetValue() == kind {
					return metric.GetCounter().GetValue()
				}
			}
		}
	}

	return 0
}
