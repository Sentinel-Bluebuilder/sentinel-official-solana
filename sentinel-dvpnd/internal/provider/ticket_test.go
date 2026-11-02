//go:build linux

package provider_test

import (
	"errors"
	"testing"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/sentinel-official/sentinel-dvpnd/internal/provider"
	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestRuntime_IssueTicket_CapsDataCap(t *testing.T) {
	t.Parallel()

	home := testutil.NewProviderHome(t, sdktest.NewFakeRPC(t))
	home.Loaded.Provider.Tickets.DataCapBytes = 500_000_000
	r := newRuntime(t, home)
	userKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	planAddress, subAddress, nodeAddress := solana.PublicKey{1}, solana.PublicKey{2}, solana.PublicKey{3}
	now := time.Now().Unix()
	seedSubscriptionAccount(t, home, subAddress, dvpn.Subscription{
		DataCapGB: 10, ExpiresAt: now + 100_000, MaxConnections: 2, OpenedAt: now - 100,
		Owner: userKey.PublicKey(), Plan: planAddress, Provider: providerAddress(t, home),
	})
	require.NoError(t, home.DB.WithContext(t.Context()).Create(&provider.RosterNode{
		Node: nodeAddress.String(), Plan: planAddress.String(),
	}).Error)
	require.NoError(t, home.DB.WithContext(t.Context()).Create(&provider.Lease{
		Address: solana.PublicKey{4}.String(), Healthy: true, Node: nodeAddress.String(),
	}).Error)

	encoded := encodeTicketRequest(t, userKey, providerAddress(t, home), subAddress, nodeAddress, now)
	result, err := provider.IssueTicket(t.Context(), r, encoded)

	require.NoError(t, err)
	require.Equal(t, uint64(500_000_000), result.DataCapBytes)
}

func TestRuntime_IssueTicket_RejectsStaleRequest(t *testing.T) {
	t.Parallel()

	home := testutil.NewProviderHome(t, sdktest.NewFakeRPC(t))
	r := newRuntime(t, home)
	userKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	planAddress, subAddress, nodeAddress := solana.PublicKey{1}, solana.PublicKey{2}, solana.PublicKey{3}
	now := time.Now().Unix()
	seedSubscriptionAccount(t, home, subAddress, dvpn.Subscription{
		DataCapGB: 10, ExpiresAt: now + 100_000, MaxConnections: 2, OpenedAt: now - 100,
		Owner: userKey.PublicKey(), Plan: planAddress, Provider: providerAddress(t, home),
	})

	stale := encodeTicketRequest(t, userKey, providerAddress(t, home), subAddress, nodeAddress, now-600)
	_, err = provider.IssueTicket(t.Context(), r, stale)

	require.ErrorIs(t, err, auth.ErrTicketRequestStale)
}

func TestRuntime_ReserveTicket_SubscriptionReadFailure(t *testing.T) {
	t.Parallel()

	home := testutil.NewProviderHome(t, sdktest.NewFakeRPC(t))
	r := newRuntime(t, home)
	wantErr := errors.New("subscription read failed")

	require.NoError(t, home.DB.Callback().Query().Before("gorm:query").Register(
		"test:subscription_failure",
		func(db *gorm.DB) {
			if db.Statement.Table == "subscriptions" {
				db.Error = wantErr
			}
		},
	))

	now := time.Now()
	_, err := provider.ReserveTicket(t.Context(), r, auth.SignedTicketRequest{
		Request: auth.TicketRequest{Subscription: solana.PublicKey{1}},
	}, dvpn.Subscription{DataCapGB: 1}, auth.Nonce{}, now, now.Unix()+60)

	require.ErrorIs(t, err, wantErr)

	var count int64
	require.NoError(t, home.DB.WithContext(t.Context()).Model(&provider.Ticket{}).Count(&count).Error)
	require.Zero(t, count)
}
