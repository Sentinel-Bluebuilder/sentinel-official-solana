package service_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

func TestPeers(t *testing.T) {
	t.Parallel()

	peers := service.NewPeers[int]()
	require.NoError(t, peers.Add("a", 1))
	require.True(t, peers.Has("a"))

	err := peers.Add("a", 2)
	require.ErrorIs(t, err, service.ErrPeerExists)
	require.EqualError(t, err, "peer already exists: a")

	got, err := peers.Get("a")
	require.NoError(t, err)
	require.Equal(t, 1, got)

	require.NoError(t, peers.Remove("a"))
	_, err = peers.Get("a")
	require.ErrorIs(t, err, service.ErrPeerNotFound)
	require.ErrorIs(t, peers.Remove("a"), service.ErrPeerNotFound)
}
