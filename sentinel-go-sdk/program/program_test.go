package program_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestLive(t *testing.T) {
	t.Parallel()

	cfg := dvpn.Config{NodeListingTTLSeconds: 300}

	listedNode := dvpn.Node{ListedAt: 1000}
	require.True(t, program.Live(listedNode, cfg, 1299))
	require.False(t, program.Live(listedNode, cfg, 1300))
	require.False(t, program.Live(dvpn.Node{}, cfg, 100))
}

func TestStreamAccrued(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		escrow      uint64
		streamBase  uint64
		ratePerHour uint64
		startedAt   int64
		now         int64
		want        uint64
	}{
		{
			name:        "ninety seconds",
			escrow:      100_000_000,
			streamBase:  0,
			ratePerHour: 10_000_000,
			startedAt:   0,
			now:         90,
			want:        250_000,
		},
		{
			name:        "capped at escrow",
			escrow:      100_000_000,
			streamBase:  0,
			ratePerHour: 10_000_000,
			startedAt:   0,
			now:         54_000,
			want:        100_000_000,
		},
		{
			name:        "clock regression keeps the stream base",
			escrow:      100_000_000,
			streamBase:  7_000_000,
			ratePerHour: 10_000_000,
			startedAt:   1000,
			now:         900,
			want:        7_000_000,
		},
	}

	for _, tc := range cases {
		got := program.StreamAccrued(tc.escrow, tc.streamBase, tc.ratePerHour, tc.startedAt, tc.now)

		require.Equal(t, tc.want, got, tc.name)
	}
}
