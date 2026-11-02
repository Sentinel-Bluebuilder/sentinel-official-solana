package session_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
)

func TestMeter(t *testing.T) {
	t.Parallel()

	terms := session.Terms{Basis: session.BasisPerGB, Price: 1_000}
	meter := session.NewMeter(terms, 150, 50)

	require.Equal(t, uint64(50), meter.Owed(session.Usage{}))
	require.Equal(t, uint64(100), meter.Headroom(session.Usage{}))
	require.Equal(t, uint64(50), meter.Settled())
	require.False(t, meter.Exhausted(session.Usage{RxBytes: 99_000_000}, 0))
	require.True(t, meter.Exhausted(session.Usage{RxBytes: 99_999_999}, 0))
	require.True(t, meter.Exhausted(session.Usage{RxBytes: 100_000_000}, 0))
	require.Zero(t, meter.Headroom(session.Usage{RxBytes: 101_000_000}))

	meter.Update(120)
	require.Equal(t, uint64(150), meter.Paid())
	meter.Update(200)
	require.Equal(t, uint64(200), meter.Paid())
	require.False(t, meter.Exhausted(session.Usage{RxBytes: 100_000_000}, 0))
	require.Equal(t, uint64(50), meter.Settled())
}

func TestMeter_Exhausted_FreePriceRetainsSettledDebt(t *testing.T) {
	t.Parallel()

	terms := session.Terms{Basis: session.BasisPerHour}
	usage := session.Usage{RxBytes: math.MaxUint64, Seconds: math.MaxUint64}
	require.False(t, session.NewMeter(terms, 0, 0).Exhausted(usage, 0))
	require.False(t, session.NewMeter(terms, 50, 50).Exhausted(usage, 0))
	require.True(t, session.NewMeter(terms, 49, 50).Exhausted(usage, 0))
}

func TestMeter_Owed_Saturates(t *testing.T) {
	t.Parallel()

	terms := session.Terms{Basis: session.BasisPerHour, Price: 3_600}
	meter := session.NewMeter(terms, math.MaxUint64, math.MaxUint64-1)

	require.Equal(t, uint64(math.MaxUint64-1), meter.Owed(session.Usage{}))
	require.False(t, meter.Exhausted(session.Usage{}, 0))
	require.Equal(t, uint64(math.MaxUint64), meter.Owed(session.Usage{Seconds: 2}))
	require.True(t, meter.Exhausted(session.Usage{Seconds: 2}, 0))
	require.Zero(t, meter.Headroom(session.Usage{Seconds: 2}))
}

func TestStreamEnforcer_Exhausted(t *testing.T) {
	t.Parallel()

	enforcer := session.NewStreamEnforcer(3_600, 0, 3_600, 0)

	require.False(t, enforcer.Exhausted(session.Usage{}, 3_599))
	require.True(t, enforcer.Exhausted(session.Usage{}, 3_600))

	enforcer.Update(7_200, 3_600, 10_000)
	require.False(t, enforcer.Exhausted(session.Usage{}, 13_599))
	require.True(t, enforcer.Exhausted(session.Usage{}, 13_600))

	free := session.NewStreamEnforcer(100, 0, 0, 0)
	require.False(t, free.Exhausted(session.Usage{}, math.MaxInt64))

	empty := session.NewStreamEnforcer(0, 0, 0, 0)
	require.True(t, empty.Exhausted(session.Usage{}, 0))
}

func TestTicketEnforcer_CapReached(t *testing.T) {
	t.Parallel()

	enforcer := session.NewTicketEnforcer(100, 1_000)
	require.False(t, enforcer.CapReached(session.Usage{RxBytes: 99}))
	require.True(t, enforcer.CapReached(session.Usage{RxBytes: 100}))

	unlimited := session.NewTicketEnforcer(0, 1_000)
	require.False(t, unlimited.CapReached(session.Usage{RxBytes: math.MaxUint64}))
}

func TestTicketEnforcer_Exhausted(t *testing.T) {
	t.Parallel()

	enforcer := session.NewTicketEnforcer(1_000, 2_000)

	usage999 := session.Usage{RxBytes: 999}
	require.False(t, enforcer.Exhausted(usage999, 1_000))

	usage1000 := session.Usage{RxBytes: 1_000}
	require.True(t, enforcer.Exhausted(usage1000, 1_000))
	require.True(t, enforcer.Exhausted(session.Usage{}, 2_000))

	enforcer.SetLeaseHealthy(false)
	require.True(t, enforcer.Exhausted(session.Usage{}, 1_000))

	unlimited := session.NewTicketEnforcer(0, 2_000)
	usageBig := session.Usage{RxBytes: 1 << 40}
	require.False(t, unlimited.Exhausted(usageBig, 1_000))
}

func TestTicketEnforcer_Expired(t *testing.T) {
	t.Parallel()

	enforcer := session.NewTicketEnforcer(100, 1_000)
	require.False(t, enforcer.Expired(999))
	require.True(t, enforcer.Expired(1_000))
}

func TestTicketEnforcer_LeaseUnhealthy(t *testing.T) {
	t.Parallel()

	enforcer := session.NewTicketEnforcer(100, 1_000)
	require.False(t, enforcer.LeaseUnhealthy())
	enforcer.SetLeaseHealthy(false)
	require.True(t, enforcer.LeaseUnhealthy())
}
