package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/client"
	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
)

func TestRuntime_Recover_PersistedKillSwitchReachesRPC(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	home := testutil.NewClientHome(t, fakeRPC)
	home.Loaded.Client.Network.KillSwitch = config.KillSwitchPersist
	ks := &fakeKillSwitchNet{base: http.DefaultTransport}
	programClient := ks.programFor(t, fakeRPC, home.ProgramClient)
	rt := newRuntime(t, home, programClient)
	client.SetLookup(rt, ks.lookup)

	firewall := &recordingFirewallBackend{onApply: ks.apply}
	client.SetFirewallBackend(rt, firewall)
	client.SetDNSBackend(rt, &recordingDNSBackend{})

	registerFakeClientOnce()

	state := sdktest.NewFakeState(sdktest.FakeName)
	client.SetService(rt, sdktest.FakeName, fakeClientConfig{state: state})

	req := seedTicketRequest(t, fakeRPC, home, home.DaemonKey.PublicKey(), time.Now().Add(time.Hour).Unix())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.StartSession(ctx, rt, req)
	require.NoError(t, err)
	require.NoError(t, rt.Stop(ctx))
	require.Zero(t, firewall.flushes())

	want, wantRPC := rpcEndpoints(t, home)
	requirePinned(t, home.Pin, wantRPC)

	restartedRT, rpcPin := restartRuntime(t, ctx, home, programClient, firewall, ks.lookup, &recordingDNSBackend{})

	applied := firewall.applies()
	require.Len(t, applied, 2)
	require.Equal(t, want, applied[1].rpcAndWSAddrs)
	requirePinned(t, rpcPin, wantRPC)

	_, err = client.StartSession(ctx, restartedRT, req)
	require.NoError(t, err)

	applied = firewall.applies()
	require.Len(t, applied, 3)
	require.Equal(t, want, applied[2].rpcAndWSAddrs)

	require.NoError(t, client.StopSession(ctx, restartedRT))
	require.Equal(t, 1, firewall.flushes())
	requireUnpinned(t, rpcPin)
}

func TestRuntime_StartSession_AdmitsRPCEndpointsAndPersists(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	home := testutil.NewClientHome(t, fakeRPC)
	rt := newRuntime(t, home, home.ProgramClient)

	firewall := &recordingFirewallBackend{}
	client.SetFirewallBackend(rt, firewall)
	client.SetDNSBackend(rt, &recordingDNSBackend{})

	registerFakeClientOnce()

	state := sdktest.NewFakeState(sdktest.FakeName)
	client.SetService(rt, sdktest.FakeName, fakeClientConfig{state: state})

	req := seedTicketRequest(t, fakeRPC, home, home.DaemonKey.PublicKey(), time.Now().Add(time.Hour).Unix())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	requireUnpinned(t, home.Pin)

	_, err := client.StartSession(ctx, rt, req)
	require.NoError(t, err)

	want, wantRPC := rpcEndpoints(t, home)

	applied := firewall.applies()
	require.Len(t, applied, 1)
	require.Equal(t, want, applied[0].rpcAndWSAddrs)
	requirePinned(t, home.Pin, wantRPC)

	var row client.Firewall
	require.NoError(t, home.DB.WithContext(t.Context()).First(&row).Error)

	storedNode := storedAddrs(t, row.NodeAddrs)
	stored := storedAddrPorts(t, row.RPCAndWSAddrs)
	storedRPC := storedAddrPorts(t, row.RPCAddrs)
	require.Equal(t, want, stored)
	require.Equal(t, wantRPC, storedRPC)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("127.0.0.1")}, storedNode)
	require.Equal(t, home.NodeServer.URL, row.NodeURI)
	require.Equal(t, storedNode, applied[0].nodeAddrs)

	require.NoError(t, client.StopSession(ctx, rt))
	requireUnpinned(t, home.Pin)
}

func TestRuntime_StartSession_FailureAfterKillSwitchFlushesIt(t *testing.T) {
	t.Parallel()

	fixture := newKillSwitchFixture(t)
	fixture.dnsBackend.failFrom = 1

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.StartSession(ctx, fixture.rt, fixture.req)
	require.ErrorIs(t, err, errDNSApplyFailed)

	require.Len(t, fixture.firewall.applies(), 1)
	require.Equal(t, 1, fixture.firewall.flushes())
	require.Empty(t, firewallRows(t, fixture.home))
	requireUnpinned(t, fixture.home.Pin)
}

func TestRuntime_StartSession_PartialFirewallFailure(t *testing.T) {
	t.Parallel()

	for _, retry := range []bool{false, true} {
		t.Run(strconv.FormatBool(retry), func(t *testing.T) {
			t.Parallel()

			fixture := newKillSwitchFixture(t)
			fixture.home.Loaded.Client.Network.KillSwitch = config.KillSwitchOn
			applyErr := errors.New("apply failed after mutation")
			flushErr := errors.New("flush failed")
			fixture.firewall.applyErr = errors.Join(client.ErrFirewallChanged, applyErr)

			if retry {
				fixture.firewall.flushErr = flushErr
			}

			_, err := client.StartSession(t.Context(), fixture.rt, fixture.req)
			require.ErrorIs(t, err, applyErr)
			require.Equal(t, 1, fixture.firewall.flushes())

			if retry {
				require.ErrorIs(t, err, flushErr)
				fixture.firewall.flushErr = nil
				require.NoError(t, client.StopSession(t.Context(), fixture.rt))
				require.Equal(t, 2, fixture.firewall.flushes())
			}

			require.Empty(t, firewallRows(t, fixture.home))
			requireUnpinned(t, fixture.home.Pin)
		})
	}
}

func TestRuntime_StartSession_PartialFirewallFailureKeepsPersistentPolicy(t *testing.T) {
	t.Parallel()

	fixture := newKillSwitchFixture(t)
	fixture.startAndSettle(t, t.Context())
	require.NoError(t, fixture.rt.Stop(t.Context()))
	rt, _ := restartRuntime(t, t.Context(), fixture.home, fixture.programClient,
		fixture.firewall, fixture.ks.lookup, &recordingDNSBackend{})
	failure := errors.New("apply failed after mutation")
	fixture.firewall.applyErr = errors.Join(client.ErrFirewallChanged, failure)

	_, err := client.StartSession(t.Context(), rt, fixture.req)
	require.ErrorIs(t, err, failure)
	require.Zero(t, fixture.firewall.flushes())
	require.Len(t, firewallRows(t, fixture.home), 1)
	require.NoError(t, client.StopSession(t.Context(), rt))
	require.Equal(t, 1, fixture.firewall.flushes())
}

func TestRuntime_StartSession_UntouchedFirewallFailure(t *testing.T) {
	t.Parallel()

	fixture := newKillSwitchFixture(t)
	failure := errors.New("apply rejected before mutation")
	fixture.firewall.applyErr = failure
	fixture.firewall.onApply = nil

	_, err := client.StartSession(t.Context(), fixture.rt, fixture.req)
	require.ErrorIs(t, err, failure)
	require.Zero(t, fixture.firewall.flushes())
}

func TestRuntime_StopSession_DuringStartLeavesNoFirewall(t *testing.T) {
	t.Parallel()

	fixture := newKillSwitchFixture(t)
	stopped := fixture.stopOnFirstStart()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.StartSession(ctx, fixture.rt, fixture.req)
	require.ErrorIs(t, err, client.ErrStoppedDuringConnect)
	require.NoError(t, awaitErr(t, ctx, stopped))

	require.Empty(t, fixture.firewall.applies())
	require.Empty(t, firewallRows(t, fixture.home))
	requireUnpinned(t, fixture.home.Pin)
}

func TestRuntime_StopSession_ReleasesRecoveredKillSwitch(t *testing.T) {
	t.Parallel()

	fixture := newKillSwitchFixture(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fixture.startAndSettle(t, ctx)
	require.NoError(t, fixture.rt.Stop(ctx))

	rt, afterRestart := restartRuntime(
		t, ctx, fixture.home, fixture.programClient, fixture.firewall, fixture.ks.lookup, &recordingDNSBackend{},
	)

	_, wantRPC := rpcEndpoints(t, fixture.home)
	requirePinned(t, afterRestart, wantRPC)

	svc := startClientService(t, rt)

	response, err := svc.StopSession(ctx, &daemonv1.StopSessionRequest{})
	require.NoError(t, err)
	require.True(t, response.GetKillSwitchReleased())

	require.Equal(t, 1, fixture.firewall.flushes())
	require.Empty(t, firewallRows(t, fixture.home))
	requireUnpinned(t, afterRestart)
}
