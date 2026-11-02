//go:build linux

package provider_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/provider"
	"github.com/sentinel-official/sentinel-dvpnd/internal/store"
	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/providerapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const planName = "plan"

func encodeTicketRequest(
	t *testing.T, userKey solana.PrivateKey, providerAddress, subscription, nodeAddress solana.PublicKey,
	requestedUnix int64,
) string {
	t.Helper()

	nonce, err := auth.NewNonce()
	require.NoError(t, err)

	request := auth.TicketRequest{
		Node:  nodeAddress,
		Nonce: nonce, Provider: providerAddress, RequestedUnix: requestedUnix,
		Subscription: subscription, Version: auth.TicketVersion,
	}

	return auth.SignTicketRequest(userKey, request).Encode()
}

func newLeaseWatchFixture(t *testing.T) (testutil.Home, *provider.Runtime, provider.Lease, provider.Probe) {
	t.Helper()
	home := testutil.NewProviderHome(t, sdktest.NewFakeRPC(t))
	r := newRuntime(t, home)
	t.Cleanup(func() { require.NoError(t, r.Stop(t.Context())) })

	node, err := home.ProgramClient.FetchNode(t.Context(), home.NodeAddress)
	require.NoError(t, err)

	lease := provider.Lease{
		Address: solana.PublicKey{99}.String(), Escrow: 100_000_000, Mode: dvpn.PositionModeVoucher,
		Node: home.NodeAddress.String(), OpenedUnix: time.Now().Unix(), RatePerHour: 3_600_000,
		NodeOpenedUnix: node.Value.OpenedAt, Status: provider.StatusOpen,
	}
	probe := provider.Probe{
		APIURL:                home.NodeServer.URL,
		HeartbeatSeconds:      1,
		Live:                  true,
		MinLeasePrepaySeconds: 60,
		Node:                  lease.Node,
		ProbedUnix:            time.Now().Unix(),
		TLSSHA256:             api.Fingerprint(home.NodeServer.Certificate()),
	}
	require.NoError(t, home.DB.Create(&lease).Error)
	require.NoError(t, home.DB.Create(&probe).Error)
	home.NodeService.SeedLease(home.DaemonKey, solana.MustPublicKeyFromBase58(lease.Address),
		0, 0, lease.Escrow, lease.RatePerHour, lease.OpenedUnix)

	return home, r, lease, probe
}

func newRuntime(t *testing.T, home testutil.Home) *provider.Runtime {
	t.Helper()

	r, err := provider.New(
		home.Loaded, home.DB, home.DaemonKey, home.ProgramClient, home.Sender, zerolog.Nop(), prometheus.NewRegistry(),
	)
	require.NoError(t, err)
	require.NoError(t, store.Migrate(t.Context(), home.DB, r.Models()...))

	return r
}

func providerAddress(t *testing.T, home testutil.Home) solana.PublicKey {
	t.Helper()

	id, err := program.ParseID(home.Loaded.Provider.ID)
	require.NoError(t, err)
	address, _, err := dvpn.FindProviderAddress(home.ProgramClient.ProgramID, home.OwnerAddress, id)
	require.NoError(t, err)

	return address
}

func seedPlanAccount(t *testing.T, home testutil.Home, address, providerAddress solana.PublicKey) {
	t.Helper()

	plan := dvpn.Plan{
		DataCapGB: 5, DurationSeconds: 3600, MaxConnections: 3, Price: 1_000,
		Provider: providerAddress, Status: dvpn.ListingStatusListed,
	}
	home.RPC.SetAccount(address, sdktest.Account{
		Data: sdktest.EncodeAccount(dvpn.PlanAccountDiscriminator, plan), Owner: home.ProgramClient.ProgramID,
	})
}

func seedSubscriptionAccount(t *testing.T, home testutil.Home, address solana.PublicKey, sub dvpn.Subscription) {
	t.Helper()
	home.RPC.SetAccount(address, sdktest.Account{
		Data: sdktest.EncodeAccount(dvpn.SubscriptionAccountDiscriminator, sub), Owner: home.ProgramClient.ProgramID,
	})
}

func startProviderAPI(t *testing.T, runtime *provider.Runtime) *providerapi.ClientWithResponses {
	t.Helper()

	server := httptest.NewServer(provider.APIHandler(runtime))
	t.Cleanup(server.Close)
	client, err := providerapi.NewInsecureClient(server.URL, 5*time.Second)
	require.NoError(t, err)

	return client
}
