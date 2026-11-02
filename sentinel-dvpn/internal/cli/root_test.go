package cli_test

import (
	"bytes"
	"io"
	"math"
	"regexp"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpn/internal/cli"
)

func TestNewRootCmd_CommandNamesAreKebabCase(t *testing.T) {
	t.Parallel()

	kebab := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

	var walk func(cmd *cobra.Command)

	walk = func(cmd *cobra.Command) {
		require.Regexp(t, kebab, cmd.Name(), cmd.CommandPath())

		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(cli.NewRootCmd())
}

func TestNewRootCmd_CompletionRequestsSkipConfigLoading(t *testing.T) {
	cases := []struct {
		name    string
		request string
	}{
		{name: "with descriptions", request: cobra.ShellCompRequestCmd},
		{name: "without descriptions", request: cobra.ShellCompNoDescRequestCmd},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SENTINEL_DVPN_OUTPUT", "invalid")

			var out bytes.Buffer

			root := cli.NewRootCmd()
			root.SetArgs([]string{"--home", t.TempDir(), tc.request, "cl"})
			root.SetOut(&out)
			root.SetErr(io.Discard)

			require.NoError(t, root.ExecuteContext(t.Context()))
			require.Contains(t, out.String(), "client")
		})
	}
}

func TestNewRootCmd_ConfigShowRedactsAPIKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		args []string
		name string
		want string
	}{
		{args: []string{"--tx.paymaster.api-key", "test-api-key"}, name: "set", want: `"api_key": "[redacted]"`},
		{name: "unset", want: `"api_key": ""`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root, out := newRoot(t, "", append([]string{"config", "show"}, tc.args...)...)

			require.NoError(t, root.ExecuteContext(t.Context()))
			require.Contains(t, out.String(), tc.want)
			require.NotContains(t, out.String(), "test-api-key")
		})
	}
}

func TestNewRootCmd_KeyListSkipsConfigLoading(t *testing.T) {
	t.Parallel()

	root := cli.NewRootCmd()
	root.SetArgs([]string{"--home", t.TempDir(), "key", "list"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	require.NoError(t, root.ExecuteContext(t.Context()))
}

func TestNewRootCmd_NodeProviderCombineChainAndDaemonCmds(t *testing.T) {
	t.Parallel()

	root := cli.NewRootCmd()

	node := findSubcommand(t, root, "node")
	requireSubcommandNames(t, node, "open-node", "close-node", "status", "sessions")
	require.NotNil(t, findSubcommand(t, node, "status").Flags().Lookup("instance"))
	require.Nil(t, findSubcommand(t, node, "inspect").Flags().Lookup("instance"))
	require.Nil(t, findSubcommand(t, node, "open-node").Flags().Lookup("instance"))

	provider := findSubcommand(t, root, "provider")
	requireSubcommandNames(t, provider, "open-provider", "close-provider", "status", "leases")
	require.Nil(t, findSubcommand(t, provider, "open-provider").Flags().Lookup("instance"))
}

func TestNewRootCmd_PaymasterBuildsForSendingCommands(t *testing.T) {
	cases := []struct {
		args    []string
		name    string
		wantErr bool
	}{
		{args: []string{"config", "show"}, name: "config show"},
		{args: []string{"grant", "close-grant"}, name: "chain command", wantErr: true},
		{args: []string{"wallet", "create-token-account"}, name: "wallet command", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SENTINEL_DVPN_TX_PAYMASTER_FEE_TOKEN", "not-an-address")
			t.Setenv("SENTINEL_DVPN_TX_PAYMASTER_URL", "http://127.0.0.1:1")

			root, _ := newRoot(t, "", tc.args...)

			err := root.ExecuteContext(t.Context())
			if tc.wantErr {
				require.ErrorContains(t, err, "building paymaster: ")

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestNewRootCmd_ProgramAndClientGroups(t *testing.T) {
	t.Parallel()

	root := cli.NewRootCmd()

	program := findSubcommand(t, root, "program")
	requireSubcommandNames(
		t, program,
		"accept-authority", "initialize-config", "propose-authority", "set-params",
		"set-paused-mask", "set-treasury", "sweep-config-fees", "show", "params", "status",
	)

	client := findSubcommand(t, root, "client")
	requireSubcommandNames(t, client, "start", "stop", "status", "watch", "sessions", "channels", "tickets")

	provider := findSubcommand(t, root, "provider")
	requireSubcommandNames(t, provider, "nodes", "add-node", "remove-node")
}

func TestNewRootCmd_WarnsAboutDaemonVariables(t *testing.T) {
	t.Setenv("SENTINEL_DVPND_RPC_URL", "http://127.0.0.1:8899")

	root, _ := newRoot(t, "", "version")

	var logs bytes.Buffer
	root.SetErr(&logs)

	require.NoError(t, root.ExecuteContext(t.Context()))
	require.Contains(t, logs.String(), "ignoring a sentinel-dvpnd variable")
	require.Contains(t, logs.String(), "SENTINEL_DVPN_RPC_URL")
	require.Contains(t, logs.String(), "SENTINEL_DVPND_RPC_URL")
}

func TestParseApproveAmount(t *testing.T) {
	t.Parallel()

	cases := []struct {
		amount  string
		name    string
		want    uint64
		wantErr bool
	}{
		{amount: "unlimited", name: "unlimited", want: math.MaxUint64},
		{amount: "1000000", name: "number", want: 1_000_000},
		{amount: "0", name: "zero", want: 0},
		{amount: "not-a-number", name: "not a number", wantErr: true},
		{amount: "-1", name: "negative", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := cli.ParseApproveAmount(tc.amount)
			if tc.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
