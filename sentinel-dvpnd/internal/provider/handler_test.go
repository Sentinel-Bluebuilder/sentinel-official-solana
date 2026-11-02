//go:build linux

package provider_test

import (
	"testing"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/provider"
	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	"github.com/sentinel-official/sentinel-go-sdk/v3/providerapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestHandler_IssueTicket(t *testing.T) {
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
	require.NoError(t, home.DB.WithContext(t.Context()).Create(&provider.RosterNode{
		Node: nodeAddress.String(), Plan: planAddress.String(),
	}).Error)
	require.NoError(t, home.DB.WithContext(t.Context()).Create(&provider.Lease{
		Address: solana.PublicKey{4}.String(), Healthy: true, Node: nodeAddress.String(),
	}).Error)
	require.NoError(t, r.Start(t.Context()))
	client := startProviderAPI(t, r)
	encoded := encodeTicketRequest(t, userKey, providerAddress(t, home), subAddress, nodeAddress, now)
	response, err := client.IssueTicketWithResponse(t.Context(), providerapi.IssueTicketJSONRequestBody{
		TicketRequest: encoded,
	})
	require.NoError(t, err)
	require.NotNil(t, response.JSON200)
	require.NotEmpty(t, response.JSON200.Ticket)
	bad, err := client.IssueTicketWithResponse(t.Context(), providerapi.IssueTicketJSONRequestBody{
		TicketRequest: "not-valid",
	})
	require.NoError(t, err)
	require.Nil(t, bad.JSON200)
}
