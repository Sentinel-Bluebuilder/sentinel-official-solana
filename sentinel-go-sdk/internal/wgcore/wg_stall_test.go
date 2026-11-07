package wgcore_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/internal/wgcore"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

// ─── A tunnel whose data path dies while the interface stays up (private I#22) ───

func startedHandshakeClient(t *testing.T, r *fakeRunner) *wgcore.Client[plainExtensions] {
	t.Helper()

	c := wgcore.NewClientForTest[plainExtensions](plainVariantFixture(), clientPlacementFixture(t), zerolog.Nop(), r)

	_, err := c.PeerRequest(t.Context())
	require.NoError(t, err)
	require.NoError(t, c.Start(t.Context(), "203.0.113.1", peerResponseFixture(t, wgcore.ClientPublicKey(c))))

	return c
}

func TestClient_Stats_StalledHandshakeReportsADeadTunnel(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		r := &fakeRunner{live: "utun7"}
		c := startedHandshakeClient(t, r)
		key := wgcore.ClientServerPublicKey(c)
		r.handshakes = map[string]time.Time{key: time.Now()}

		// Healthy and idle: the counters don't move, but the keepalive renews the handshake every 2 minutes.
		for range 5 {
			time.Sleep(2 * time.Minute)
			r.handshakes[key] = time.Now()

			_, err := c.Stats(t.Context())
			require.NoError(t, err)
		}

		// The node stops answering on the data path: the last handshake stays where it is.
		time.Sleep(4 * time.Minute)

		_, err := c.Stats(t.Context())
		require.NoError(t, err, "exactly 4 minutes is still within the limit")

		time.Sleep(10 * time.Second)

		_, err = c.Stats(t.Context())
		require.ErrorIs(t, err, service.ErrTunnelStalled)
		require.ErrorContains(t, err, "no handshake with the node for 4m10s")
	})
}

func TestClient_Stats_NoHandshakeSinceTheStartReportsADeadTunnel(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		r := &fakeRunner{live: "utun7", handshakes: map[string]time.Time{}}
		c := startedHandshakeClient(t, r)

		time.Sleep(3*time.Minute + 59*time.Second)

		_, err := c.Stats(t.Context())
		require.NoError(t, err)

		time.Sleep(10 * time.Second)

		_, err = c.Stats(t.Context())
		require.ErrorIs(t, err, service.ErrTunnelStalled)
	})
}

func TestClient_Stats_ReadsTheHandshakeAtMostOncePerPoll(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		r := &fakeRunner{live: "utun7"}
		c := startedHandshakeClient(t, r)
		r.handshakes = map[string]time.Time{wgcore.ClientServerPublicKey(c): time.Now()}

		for range 10 {
			_, err := c.Stats(t.Context())
			require.NoError(t, err)

			time.Sleep(400 * time.Millisecond)
		}

		require.Equal(t, 1, r.handshakeHits, "one handshake read per 5 s, not one per sample")
	})
}

func TestParseLatestHandshakes(t *testing.T) {
	t.Parallel()

	got, err := wgcore.ParseLatestHandshakes("key-1\t1700000000\nkey-2\t0\n")
	require.NoError(t, err)
	require.Equal(t, map[string]time.Time{"key-1": time.Unix(1700000000, 0), "key-2": {}}, got)

	_, err = wgcore.ParseLatestHandshakes("key-1\tsoon\n")
	require.ErrorContains(t, err, "parsing latest handshakes")
}
