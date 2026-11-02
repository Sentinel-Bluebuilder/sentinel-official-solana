//go:build linux

package node_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/node"
)

func TestEventHub_PublishDuringUnsubscribe(t *testing.T) {
	t.Parallel()

	hub := node.NewEventHub()
	_, unsubscribe := hub.Subscribe("subject")

	var workers sync.WaitGroup
	workers.Go(func() {
		for range 100 {
			hub.Publish("subject", []byte("frame"))
		}
	})
	workers.Go(unsubscribe)
	workers.Go(unsubscribe)
	workers.Wait()
}

func TestEventHub_PublishReachesSubscribers(t *testing.T) {
	t.Parallel()

	hub := node.NewEventHub()
	first, unsubscribe := hub.Subscribe("subject")

	second, stop := hub.Subscribe("subject")
	defer stop()

	frame := []byte("frame")

	hub.Publish("subject", frame)
	require.Len(t, first, 1)
	require.Equal(t, frame, <-first)
	require.Len(t, second, 1)
	require.Equal(t, frame, <-second)
	unsubscribe()
	unsubscribe()
	hub.Publish("subject", frame)
	require.Len(t, second, 1)
	require.Equal(t, frame, <-second)
	require.Empty(t, first)
	hub.Publish("unknown", frame)

	for range 9 {
		hub.Publish("subject", frame)
	}

	require.Len(t, second, 8)
}
