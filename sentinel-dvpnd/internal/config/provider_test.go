package config_test

import (
	"testing"

	"github.com/creasty/defaults"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
)

func TestDefaultProvider(t *testing.T) {
	t.Parallel()

	var want config.Provider
	require.NoError(t, defaults.Set(&want))
	require.Equal(t, want, config.DefaultProvider())
}
