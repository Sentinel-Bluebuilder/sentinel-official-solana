package client_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/client"
	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
)

func TestRuntime_StartSession_DialsOnChainURI(t *testing.T) {
	t.Parallel()

	home := testutil.NewClientHome(t, sdktest.NewFakeRPC(t))
	rt, _, req := newConnectRuntime(t, home)

	decoy, decoyRequests := startCountingServer(t)

	node, err := home.ProgramClient.FetchNode(t.Context(), home.NodeAddress)
	require.NoError(t, err)
	metadata, _, err := nodeapi.Discover(t.Context(), home.ProgramClient, home.NodeAddress, node.Value, time.Second)
	require.NoError(t, err)

	metadata.API.URLs = []string{decoy.URL, home.NodeServer.URL}
	home.NodeService.SetMetadata(metadata)

	_, err = client.StartSession(t.Context(), rt, req)
	require.NoError(t, err)
	require.Zero(t, decoyRequests.Load())
	require.Equal(t, 1, home.NodeService.OpenedCount())
}

func TestRuntime_StartSession_RefusesPersistedPrivateNodeWithoutOptIn(t *testing.T) {
	t.Parallel()

	fixture := newKillSwitchFixture(t)
	fixture.startAndSettle(t, t.Context())
	require.NoError(t, fixture.rt.Stop(t.Context()))
	fixture.home.Loaded.File.Peer.AllowPrivate = false
	rt, _ := restartRuntime(t, t.Context(), fixture.home, fixture.programClient,
		fixture.firewall, fixture.ks.lookup, &recordingDNSBackend{})

	_, err := client.StartSession(t.Context(), rt, fixture.req)
	require.ErrorIs(t, err, api.ErrAddrNotPublic)
	require.Len(t, fixture.firewall.applies(), 2)
}

func TestRuntime_StartSession_RefusesPrivateNodeWithoutOptIn(t *testing.T) {
	t.Parallel()

	home := testutil.NewClientHome(t, sdktest.NewFakeRPC(t))
	home.Loaded.File.Peer.AllowPrivate = false
	rt, firewall, req := newConnectRuntime(t, home)
	node, nodeRequests := startCountingServer(t)
	seedNodeURI(t, home, node.URL)

	_, err := client.StartSession(t.Context(), rt, req)
	require.ErrorIs(t, err, api.ErrAddrNotPublic)
	require.Zero(t, nodeRequests.Load())
	require.Empty(t, firewall.applies())

	var sessions int64
	require.NoError(t, home.DB.WithContext(t.Context()).Model(&client.Session{}).Count(&sessions).Error)
	require.Zero(t, sessions)
}

func TestRuntime_StartSession_RejectsEmptyNodeURI(t *testing.T) {
	t.Parallel()

	home := testutil.NewClientHome(t, sdktest.NewFakeRPC(t))
	rt, firewall, req := newConnectRuntime(t, home)

	decoy, decoyRequests := startCountingServer(t)

	node, err := home.ProgramClient.FetchNode(t.Context(), home.NodeAddress)
	require.NoError(t, err)
	metadata, _, err := nodeapi.Discover(t.Context(), home.ProgramClient, home.NodeAddress, node.Value, time.Second)
	require.NoError(t, err)

	metadata.API.URLs = []string{decoy.URL}
	home.NodeService.SetMetadata(metadata)
	seedNodeURI(t, home, "")

	_, err = client.StartSession(t.Context(), rt, req)
	require.ErrorIs(t, err, api.ErrInvalidPeerURL)
	require.Empty(t, firewall.applies())
	require.Zero(t, decoyRequests.Load())
	require.Zero(t, home.NodeService.OpenedCount())
}

func TestRuntime_StartSession_RejectsInvalidServiceMetadata(t *testing.T) {
	t.Parallel()

	home := testutil.NewClientHome(t, sdktest.NewFakeRPC(t))
	rt, _, req := newConnectRuntime(t, home)

	node, err := home.ProgramClient.FetchNode(t.Context(), home.NodeAddress)
	require.NoError(t, err)
	metadata, _, err := nodeapi.Discover(t.Context(), home.ProgramClient, home.NodeAddress, node.Value, time.Second)
	require.NoError(t, err)

	metadata.Service.Payload = []byte(`{"port":"invalid"}`)
	home.NodeService.SetMetadata(metadata)

	var typeErr *json.UnmarshalTypeError

	_, err = client.StartSession(t.Context(), rt, req)
	require.ErrorAs(t, err, &typeErr)
	require.Zero(t, home.NodeService.OpenedCount())
}

func TestRuntime_StartSession_RejectsMetadataPriceMismatch(t *testing.T) {
	t.Parallel()

	home := testutil.NewClientHome(t, sdktest.NewFakeRPC(t))
	rt, _, req := newConnectRuntime(t, home)

	node, err := home.ProgramClient.FetchNode(t.Context(), home.NodeAddress)
	require.NoError(t, err)
	metadata, _, err := nodeapi.Discover(t.Context(), home.ProgramClient, home.NodeAddress, node.Value, time.Second)
	require.NoError(t, err)

	metadata.Settlement.Prices.ChannelPricePerGB = node.Value.ChannelPricePerGB + 1
	home.NodeService.SetMetadata(metadata)

	_, err = client.StartSession(t.Context(), rt, req)
	require.ErrorIs(t, err, session.ErrNodePriceChanged)
	require.Zero(t, home.NodeService.OpenedCount())
}

func TestRuntime_StartSession_RejectsWrongPeerName(t *testing.T) {
	t.Parallel()

	home := testutil.NewClientHome(t, sdktest.NewFakeRPC(t))
	rt, _, req := newConnectRuntime(t, home)
	home.NodeService.SetPeerName(otherFakeName)

	_, err := client.StartSession(t.Context(), rt, req)
	require.ErrorIs(t, err, service.ErrInvalidPeer)
	require.Equal(t, 1, home.NodeService.ClosedCount())
}

func newConnectRuntime(
	t *testing.T,
	home testutil.Home,
) (*client.Runtime, *recordingFirewallBackend, *daemonv1.StartSessionRequest) {
	t.Helper()

	rt := newRuntime(t, home, home.ProgramClient)
	firewall := &recordingFirewallBackend{}
	client.SetFirewallBackend(rt, firewall)
	client.SetDNSBackend(rt, &recordingDNSBackend{})
	registerFakeClientOnce()
	client.SetService(rt, sdktest.FakeName, fakeClientConfig{state: sdktest.NewFakeState(sdktest.FakeName)})

	req := seedTicketRequest(t, home.RPC, home, home.DaemonKey.PublicKey(), time.Now().Add(time.Hour).Unix())

	return rt, firewall, req
}

func seedNodeURI(t *testing.T, home testutil.Home, nodeURL string) {
	t.Helper()

	node, err := home.ProgramClient.FetchNode(t.Context(), home.NodeAddress)
	require.NoError(t, err)

	node.Value.URI = [128]uint8{}
	node.Value.URILen = uint8(copy(node.Value.URI[:], nodeURL))
	home.RPC.SetNode(home.ProgramClient.ProgramID, home.NodeAddress, node.Value)
}

func startCountingServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()

	var requests atomic.Int64

	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	t.Cleanup(server.Close)

	return server, &requests
}
