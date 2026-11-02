package cli_test

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
)

func TestNewCompletionCmd_RegistersShortNames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want []string
	}{
		{
			name: "bash",
			want: []string{"-F __start_sentinel-dvpn sentinel-dvpn", "-F __start_dvpn dvpn"},
		},
		{
			name: "fish",
			want: []string{"complete -c sentinel-dvpn -e", "complete -c dvpn -e"},
		},
		{
			name: "powershell",
			want: []string{"-CommandName 'sentinel-dvpn'", "-CommandName 'dvpn'"},
		},
		{
			name: "zsh",
			want: []string{"#compdef sentinel-dvpn dvpn\n", "compdef _sentinel-dvpn sentinel-dvpn", "compdef _dvpn dvpn"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := &cobra.Command{Use: "sentinel-dvpn"}
			root.AddCommand(cli.NewCompletionCmd("dvpn"))

			var out bytes.Buffer

			root.SetOut(&out)
			root.SetArgs([]string{"completion", tc.name})

			require.NoError(t, root.Execute())

			for _, want := range tc.want {
				require.Contains(t, out.String(), want)
			}

			require.Equal(t, "sentinel-dvpn", root.Use)
		})
	}
}
