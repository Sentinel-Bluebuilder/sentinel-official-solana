//go:build integration

package sdktest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
)

func TestPortSet_Reserve_IsExclusive(t *testing.T) {
	t.Parallel()

	portSet := sdktest.NewPortSetForTest()

	require.True(t, sdktest.ReservePortsForTest(portSet, 5000))
	require.False(t, sdktest.ReservePortsForTest(portSet, 5000))
	require.True(t, sdktest.ReservePortsForTest(portSet, 5001, 5002))
	require.False(t, sdktest.ReservePortsForTest(portSet, 5002, 5003))
	require.True(t, sdktest.ReservePortsForTest(portSet, 5003))
}
