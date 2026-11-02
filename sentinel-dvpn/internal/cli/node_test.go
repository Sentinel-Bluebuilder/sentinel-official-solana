package cli_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpn/internal/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	sdkcli "github.com/sentinel-official/sentinel-go-sdk/v3/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/wireguard"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

//nolint:paralleltest
func TestInspectNode_RefusesPrivateNodeWithoutOptIn(t *testing.T) {
	fakeRPC := sdktest.NewFakeRPC(t)
	fixture := newRunnerFixture(t, fakeRPC)
	seedConfigAccount(t, fakeRPC, dvpn.Config{Mint: testMint})

	nodeKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	srv, svc := startNodeAPI(t)

	uri, uriLen := packURI(srv.URL)
	fakeRPC.SetNode(dvpn.ProgramID, nodeKey.PublicKey(), dvpn.Node{URI: uri, URILen: uriLen})

	_, err = cli.InspectNode(
		t.Context(),
		fixture.programClient,
		fixture.signer,
		nodeKey.PublicKey(),
		sdkcli.PeerConfig{},
	)
	require.ErrorIs(t, err, api.ErrAddrNotPublic)
	require.Zero(t, svc.healthRequests.Load())
}

//nolint:paralleltest
func TestInspectNode_RejectsUngrantedOperator(t *testing.T) {
	fakeRPC := sdktest.NewFakeRPC(t)
	fixture := newRunnerFixture(t, fakeRPC)
	chainCfg := dvpn.Config{Mint: testMint}
	seedConfigAccount(t, fakeRPC, chainCfg)

	ownerKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	operatorKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	nodeKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	nodeAddress := nodeKey.PublicKey()

	srv, svc := startNodeAPI(t)

	uri, uriLen := packURI(srv.URL)
	node := dvpn.Node{
		Owner:  ownerKey.PublicKey(),
		URI:    uri,
		URILen: uriLen,
	}
	fakeRPC.SetNode(dvpn.ProgramID, nodeAddress, node)

	now := time.Now()
	metadata := nodeapi.Metadata{
		API:        nodeapi.APIInfo{URLs: []string{srv.URL}, TLSSHA256: api.Fingerprint(srv.Certificate())},
		ExpiresAt:  now.Add(auth.MetadataTTL).Unix(),
		IssuedAt:   now.Unix(),
		Location:   nodeapi.Location{Country: "DE"},
		Node:       nodeAddress,
		Operator:   operatorKey.PublicKey(),
		Owner:      ownerKey.PublicKey(),
		ProgramID:  dvpn.ProgramID,
		SDKVersion: "test-version",
		Service:    nodeapi.ServiceMetadata{Name: wireguard.Name, Payload: json.RawMessage(`{}`)},
		Settlement: nodeapi.Settlement{
			HeartbeatSeconds:        5,
			MinChannelPrepayBytes:   1,
			MinChannelPrepaySeconds: 1,
			MinLeasePrepaySeconds:   1,
		},
		Version: auth.MetadataVersion,
	}

	signedMetadata, err := nodeapi.SignMetadata(operatorKey, metadata)
	require.NoError(t, err)

	svc.metadata = signedMetadata

	_, err = cli.InspectNode(
		t.Context(),
		fixture.programClient,
		fixture.signer,
		nodeAddress,
		sdkcli.PeerConfig{AllowPrivate: true},
	)
	require.Error(t, err)
}

//nolint:paralleltest
func TestInspectNode_VerifiesMetadataThenPins(t *testing.T) {
	fakeRPC := sdktest.NewFakeRPC(t)
	fixture := newRunnerFixture(t, fakeRPC)
	chainCfg := dvpn.Config{Mint: testMint}
	seedConfigAccount(t, fakeRPC, chainCfg)

	ownerKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	operatorKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	nodeKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	nodeAddress := nodeKey.PublicKey()

	srv, svc := startNodeAPI(t)

	uri, uriLen := packURI(srv.URL)
	node := dvpn.Node{
		Owner:  ownerKey.PublicKey(),
		URI:    uri,
		URILen: uriLen,
	}
	fakeRPC.SetNode(dvpn.ProgramID, nodeAddress, node)
	seedGrantAccount(t, fakeRPC, ownerKey.PublicKey(), operatorKey.PublicKey(), program.PermSignNodeMetadata)

	now := time.Now()
	metadata := nodeapi.Metadata{
		API:        nodeapi.APIInfo{URLs: []string{srv.URL}, TLSSHA256: api.Fingerprint(srv.Certificate())},
		ExpiresAt:  now.Add(auth.MetadataTTL).Unix(),
		IssuedAt:   now.Unix(),
		Location:   nodeapi.Location{Country: "DE"},
		Node:       nodeAddress,
		Operator:   operatorKey.PublicKey(),
		Owner:      ownerKey.PublicKey(),
		ProgramID:  dvpn.ProgramID,
		SDKVersion: "test-version",
		Service:    nodeapi.ServiceMetadata{Name: wireguard.Name, Payload: json.RawMessage(`{}`)},
		Settlement: nodeapi.Settlement{
			HeartbeatSeconds:        5,
			MinChannelPrepayBytes:   1,
			MinChannelPrepaySeconds: 1,
			MinLeasePrepaySeconds:   1,
		},
		Version: auth.MetadataVersion,
	}

	signedMetadata, err := nodeapi.SignMetadata(operatorKey, metadata)
	require.NoError(t, err)

	svc.metadata = signedMetadata

	out, err := cli.InspectNode(
		t.Context(),
		fixture.programClient,
		fixture.signer,
		nodeAddress,
		sdkcli.PeerConfig{AllowPrivate: true},
	)

	require.NoError(t, err)
	require.Equal(t, nodeapi.HealthStatus("ok"), out.Health.Status)
	require.Equal(t, nodeAddress, out.Metadata.Node)
	require.Equal(t, ownerKey.PublicKey(), out.Metadata.Owner)
}
