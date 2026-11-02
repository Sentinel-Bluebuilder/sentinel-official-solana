package cli_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpn/internal/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
)

func TestDialer_Dial(t *testing.T) {
	t.Parallel()

	cases := []struct {
		endpoint string
		name     string
		tag      instance.Tag
		wantErr  string
	}{
		{name: "instance not found", tag: "c0", wantErr: "instance c0 not found; available: n0, n1"},
		{name: "instance found", tag: "n0"},
		{endpoint: "/run/sentinel-dvpnd.sock", name: "endpoint override", tag: "c0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			for _, name := range []string{"n0", "n1"} {
				require.NoError(t, os.Mkdir(filepath.Join(root, name), 0o750))
			}

			err := cli.DialerDial(tc.endpoint, root, t.Context(), tc.tag)
			if tc.wantErr != "" {
				require.ErrorIs(t, err, instance.ErrInstanceNotFound)
				require.EqualError(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestDialer_EndpointFor(t *testing.T) {
	t.Parallel()

	require.Equal(t, instance.Tag("c0").Endpoint(runtime.GOOS), cli.DialerEndpointFor("", "c0"))
	require.Equal(t, "/run/sentinel-dvpnd.sock", cli.DialerEndpointFor("/run/sentinel-dvpnd.sock", "c0"))
}
