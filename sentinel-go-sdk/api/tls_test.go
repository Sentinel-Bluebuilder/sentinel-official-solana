package api_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
)

func TestFormatColonFingerprint(t *testing.T) {
	t.Parallel()

	actual := api.FormatColonFingerprint("abcdef")
	require.Equal(t, "AB:CD:EF", actual)
}

func TestLoadOrCreateServerCert(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	cert, fingerprint, err := api.LoadOrCreateServerCert(dir)
	require.NoError(t, err)
	require.Len(t, fingerprint, 64)

	again, secondFingerprint, err := api.LoadOrCreateServerCert(dir)
	require.NoError(t, err)
	require.Equal(t, fingerprint, secondFingerprint)
	require.Equal(t, cert.Certificate[0], again.Certificate[0])
}
