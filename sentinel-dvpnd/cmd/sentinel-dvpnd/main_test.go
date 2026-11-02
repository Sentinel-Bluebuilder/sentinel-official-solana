package main_test

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	main "github.com/sentinel-official/sentinel-dvpnd/cmd/sentinel-dvpnd"
)

func TestNewRootCmd_RequiresInstance(t *testing.T) {
	t.Parallel()

	cases := []struct {
		args    []string
		name    string
		wantErr error
	}{
		{args: []string{"init", "--owner", randomOwner(t), "--plain-key"}, name: "init", wantErr: main.ErrMissingInstance},
		{args: []string{"run"}, name: "run", wantErr: main.ErrMissingInstance},
		{args: []string{"doctor"}, name: "doctor", wantErr: main.ErrMissingInstance},
		{args: []string{"key", "list"}, name: "key list", wantErr: main.ErrMissingInstance},
		{args: []string{"service", "install"}, name: "service", wantErr: main.ErrMissingInstance},
		{args: []string{"version"}, name: "version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cmd := main.NewRootCmd()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(tc.args)

			err := cmd.ExecuteContext(t.Context())
			if tc.wantErr == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}
