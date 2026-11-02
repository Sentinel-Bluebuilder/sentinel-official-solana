package version_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/version"
)

//nolint:paralleltest
func TestCurrent(t *testing.T) {
	previousCommit, previousTag, previousNetwork := version.Commit, version.Tag, version.Network

	t.Cleanup(func() {
		version.Commit, version.Tag, version.Network = previousCommit, previousTag, previousNetwork
	})

	version.Commit = "test-commit"
	version.Tag = "v1.2.3"
	version.Network = "test-network"

	got := version.Current()
	require.Equal(t, "test-commit", got.Commit)
	require.Equal(t, "v1.2.3", got.Tag)
	require.Equal(t, "test-network", got.Network)
}

//nolint:paralleltest // Mutates package build metadata.
func TestParseProgramID(t *testing.T) {
	previous := version.ProgramID

	t.Cleanup(func() { version.ProgramID = previous })

	cases := []struct {
		name      string
		programID string
	}{
		{name: "valid", programID: "SentineLDVPN1111111111111111111111111111111"},
		{name: "unset", programID: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			version.ProgramID = tc.programID

			got, err := version.ParseProgramID()
			if tc.programID == "" {
				require.ErrorIs(t, err, version.ErrVersionUnset)

				return
			}

			require.NoError(t, err)
			require.Equal(t, version.ProgramID, got.String())
		})
	}
}
