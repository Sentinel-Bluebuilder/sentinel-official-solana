package service_test

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
)

func TestNewPool_RejectsOverlappingPrefixes(t *testing.T) {
	t.Parallel()

	pool, err := service.NewPool(
		netip.MustParsePrefix("10.0.0.1/24"),
		netip.MustParsePrefix("10.0.0.2/25"),
	)
	require.ErrorIs(t, err, validate.ErrInvalidConfig)
	require.Nil(t, pool)
}

func TestParsePrefixes(t *testing.T) {
	t.Parallel()

	values := []string{"10.0.0.1/24", "fd00::1/64"}

	got, err := service.ParsePrefixes(values)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "10.0.0.1/24", got[0].String())

	bad := []string{"10.0.0.1"}

	_, err = service.ParsePrefixes(bad)
	require.ErrorIs(t, err, validate.ErrInvalidConfig)
	require.Contains(t, err.Error(), `address "10.0.0.1"`)
}

func TestPool_Acquire(t *testing.T) {
	t.Parallel()

	prefixV4 := netip.MustParsePrefix("10.0.0.1/29")
	prefixV6 := netip.MustParsePrefix("fd00::1/125")

	pool, err := service.NewPool(prefixV4, prefixV6)
	require.NoError(t, err)

	acquired := make([][]netip.Prefix, 0, 5)

	for range 5 {
		prefixes, err := pool.Acquire()
		require.NoError(t, err)

		acquired = append(acquired, prefixes)
	}

	require.Equal(t, "10.0.0.2/32", acquired[0][0].String())
	require.Equal(t, "fd00::2/128", acquired[0][1].String())
	require.Equal(t, "10.0.0.3/32", acquired[1][0].String())

	_, err = pool.Acquire()
	require.ErrorIs(t, err, service.ErrPoolExhausted)

	pool.Release(acquired[0])

	reused, err := pool.Acquire()
	require.NoError(t, err)
	require.Equal(t, acquired[0], reused)
}

func TestPool_Reserve(t *testing.T) {
	t.Parallel()

	pool, err := service.NewPool(netip.MustParsePrefix("10.0.0.1/24"), netip.MustParsePrefix("fd00::1/64"))
	require.NoError(t, err)
	require.NoError(t, pool.Reserve(netip.MustParseAddr("10.0.0.3")))
	require.NoError(t, pool.Reserve(netip.MustParseAddr("fd00::3")))

	first, err := pool.Acquire()
	require.NoError(t, err)
	require.Equal(t, "10.0.0.2/32", first[0].String())
	require.Equal(t, "fd00::2/128", first[1].String())

	second, err := pool.Acquire()
	require.NoError(t, err)
	require.Equal(t, "10.0.0.4/32", second[0].String())
	require.Equal(t, "fd00::4/128", second[1].String())
	require.ErrorIs(t, pool.Reserve(netip.MustParseAddr("10.0.0.3")), service.ErrAddressInUse)
	require.ErrorIs(t, pool.Reserve(netip.MustParseAddr("10.0.0.1")), service.ErrAddressInUse)
	require.ErrorIs(t, pool.Reserve(netip.MustParseAddr("10.1.0.3")), validate.ErrInvalidConfig)
}
