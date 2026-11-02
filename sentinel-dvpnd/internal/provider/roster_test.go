//go:build linux

package provider_test

import (
	"testing"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/provider"
	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
)

func TestRuntime_AddRosterNodes_RejectsForeignOwnedAccounts(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{"plan", "node"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()

			home := testutil.NewProviderHome(t, sdktest.NewFakeRPC(t))
			r := newRuntime(t, home)
			foreignOwned := solana.PublicKey{1}
			home.RPC.SetAccount(foreignOwned, sdktest.Account{Owner: solana.SystemProgramID})

			planAddress := solana.PublicKey{2}
			seedPlanAccount(t, home, planAddress, providerAddress(t, home))

			if scenario == "plan" {
				planAddress = foreignOwned
			}

			err := provider.AddRosterNodes(t.Context(), r, planAddress, []solana.PublicKey{foreignOwned})
			require.ErrorIs(t, err, daemonv1.ErrNotFound)
			require.ErrorContains(t, err, "account owned by another program")
		})
	}
}

func TestRuntime_AddRosterNodes_RejectsPlanOfOtherProvider(t *testing.T) {
	t.Parallel()

	home := testutil.NewProviderHome(t, sdktest.NewFakeRPC(t))
	r := newRuntime(t, home)
	planAddress := solana.PublicKey{1}
	otherProvider := solana.PublicKey{2}
	seedPlanAccount(t, home, planAddress, otherProvider)
	err := provider.AddRosterNodes(t.Context(), r, planAddress, nil)
	require.ErrorIs(t, err, daemonv1.ErrFailedPrecondition)
}

func TestRuntime_ProbeNodes_RefusesPrivateNodeWithoutOptIn(t *testing.T) {
	t.Parallel()
	home := testutil.NewProviderHome(t, sdktest.NewFakeRPC(t))
	home.Loaded.File.Peer.AllowPrivate = false
	r := newRuntime(t, home)

	require.False(t, probeRosterNode(t, home, r).Live)
}

func TestRuntime_ProbeNodes_UsesOnChainURI(t *testing.T) {
	t.Parallel()
	home := testutil.NewProviderHome(t, sdktest.NewFakeRPC(t))
	r := newRuntime(t, home)
	node, err := home.ProgramClient.FetchNode(t.Context(), home.NodeAddress)
	require.NoError(t, err)
	metadata, _, err := nodeapi.Discover(t.Context(), home.ProgramClient, home.NodeAddress, node.Value, time.Second)
	require.NoError(t, err)

	metadata.API.URLs = []string{"https://203.0.113.9"}
	home.NodeService.SetMetadata(metadata)

	probe := probeRosterNode(t, home, r)
	require.True(t, probe.Live)
	require.Equal(t, home.NodeServer.URL, probe.APIURL)
}

func probeRosterNode(t *testing.T, home testutil.Home, r *provider.Runtime) provider.Probe {
	t.Helper()
	require.NoError(t, home.DB.Create(&provider.RosterNode{Node: home.NodeAddress.String(), Plan: planName}).Error)
	require.NoError(t, provider.ProbeNodes(t.Context(), r))

	var probe provider.Probe
	require.NoError(t, home.DB.First(&probe, "node = ?", home.NodeAddress.String()).Error)

	return probe
}
