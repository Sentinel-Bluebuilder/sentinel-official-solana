package service_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

func TestRandomPort(t *testing.T) {
	t.Parallel()

	for range 100 {
		require.GreaterOrEqual(t, service.RandomPort(), uint16(1024))

		prefix := service.RandomPrefix()
		require.Equal(t, 24, prefix.Bits())
		require.True(t, prefix.Addr().Is4())
		require.Equal(t, uint8(10), prefix.Addr().As4()[0])
		require.Equal(t, uint8(1), prefix.Addr().As4()[3])

		value := service.RandomUint32(5, 7)
		require.GreaterOrEqual(t, value, uint32(5))
		require.LessOrEqual(t, value, uint32(7))
	}
}
