//go:build linux

package node_test

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/node"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
)

func TestRuntime_BuildMetadata(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	metadata, err := node.BuildMetadata(t.Context(), r)
	require.NoError(t, err)
	require.Equal(t, home.ProgramClient.ProgramID, metadata.ProgramID)
	require.Equal(t, home.NodeAddress, metadata.Node)
	require.Equal(t, home.OwnerAddress, metadata.Owner)
	port := strconv.FormatUint(uint64(home.Loaded.Node.API.Port), 10)
	wantURL := "https://" + net.JoinHostPort(home.Loaded.Node.API.Hosts[0], port)
	require.Equal(t, []string{wantURL}, metadata.API.URLs)
	require.Equal(t, nodeapi.Settlement{
		HeartbeatSeconds:        5,
		MinChannelPrepayBytes:   100_000_000,
		MinChannelPrepaySeconds: 60,
		MinLeasePrepaySeconds:   3600,
		Prices: nodeapi.Prices{
			ChannelPricePerGB:   5_000,
			ChannelPricePerHour: 200_000,
			LeasePricePerHour:   150_000,
		},
	}, metadata.Settlement)
	signed, err := nodeapi.SignMetadata(home.DaemonKey, metadata)
	require.NoError(t, err)
	verified, err := nodeapi.VerifyMetadata(
		t.Context(),
		home.ProgramClient,
		signed,
		home.NodeAddress,
		home.OwnerAddress,
		home.ProgramClient.ProgramID,
		time.Now(),
	)
	require.NoError(t, err)
	require.Equal(t, metadata, verified)
}

func TestRuntime_RefreshMetadata_SkipsWhenChainUnreachable(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	require.NoError(t, node.RefreshMetadata(t.Context(), r))
	first := node.CurrentMetadata(r)

	_, chainNode := node.ChainState(r)

	home.RPC.SetAccount(home.NodeAddress, sdktest.Account{})

	err := node.RefreshMetadata(t.Context(), r)
	require.ErrorIs(t, err, node.ErrNodeNotOpen)
	require.Equal(t, first, node.CurrentMetadata(r))

	repriced := chainNode
	repriced.ChannelPricePerGB++
	home.RPC.SetNode(home.ProgramClient.ProgramID, home.NodeAddress, repriced)
	require.NoError(t, node.RefreshMetadata(t.Context(), r))

	var refreshed nodeapi.Metadata
	require.NoError(t, node.CurrentMetadata(r).Decode(&refreshed))
	require.Equal(t, repriced.ChannelPricePerGB, refreshed.Settlement.Prices.ChannelPricePerGB)
}
