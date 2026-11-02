package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpn/internal/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestNewNodeSearchCmd(t *testing.T) {
	t.Parallel()

	t.Run("live", func(t *testing.T) {
		t.Parallel()

		fakeRPC := sdktest.NewFakeRPC(t)

		const ttl = 3600

		chainCfg := dvpn.Config{
			Mint:                  testMint,
			NodeListingTTLSeconds: ttl,
		}
		seedConfigAccount(t, fakeRPC, chainCfg)

		liveKey, err := solana.NewRandomPrivateKey()
		require.NoError(t, err)

		live := dvpn.Node{
			ListedAt: time.Now().Unix() - 10,
			Status:   dvpn.ListingStatusListed,
		}
		fakeRPC.SetNode(dvpn.ProgramID, liveKey.PublicKey(), live)

		expiredKey, err := solana.NewRandomPrivateKey()
		require.NoError(t, err)

		expired := dvpn.Node{
			ListedAt: time.Now().Unix() - ttl*2,
			Status:   dvpn.ListingStatusListed,
		}
		fakeRPC.SetNode(dvpn.ProgramID, expiredKey.PublicKey(), expired)

		fixture := newRunnerFixture(t, fakeRPC)

		cmd := cli.NewNodeSearchCmd(&fixture.programClient.Client, fixture.printer.Printer)

		args := []string{"--live"}
		cmd.SetArgs(args)

		require.NoError(t, cmd.Execute())

		var nodes []struct {
			Address solana.PublicKey `json:"address"`
		}

		err = json.Unmarshal(fixture.printer.last, &nodes)
		require.NoError(t, err)
		require.Len(t, nodes, 1)
		require.Equal(t, liveKey.PublicKey(), nodes[0].Address)
	})
	t.Run("unknown status", func(t *testing.T) {
		t.Parallel()

		fakeRPC := sdktest.NewFakeRPC(t)
		chainCfg := dvpn.Config{Mint: testMint}
		seedConfigAccount(t, fakeRPC, chainCfg)

		fixture := newRunnerFixture(t, fakeRPC)

		cmd := cli.NewNodeSearchCmd(&fixture.programClient.Client, fixture.printer.Printer)
		cmd.SetArgs([]string{"--status", "bogus"})

		require.ErrorIs(t, cmd.Execute(), cli.ErrUnknownStatus)
	})
}

func TestNewProgramStatusCmd(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	seedConfigAccount(t, fakeRPC, dvpn.Config{Mint: testMint, PausedMask: dvpn.PauseNodeOpens | dvpn.PauseChannelOpens})
	fixture := newRunnerFixture(t, fakeRPC)
	cmd := cli.NewProgramStatusCmd(&fixture.programClient.Client, fixture.printer.Printer)
	cmd.SetArgs([]string{})

	require.NoError(t, cmd.Execute())

	var got map[string]json.RawMessage

	require.NoError(t, json.Unmarshal(fixture.printer.last, &got))

	var features []string

	require.NoError(t, json.Unmarshal(got["paused_features"], &features))
	require.Equal(t, []string{"node-opens", "channel-opens"}, features)
	require.NotContains(t, got, "paused_mask")
}

func TestNewSearchCmd_ProviderStatusFiltersDelisted(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	chainCfg := dvpn.Config{Mint: testMint}
	seedConfigAccount(t, fakeRPC, chainCfg)

	delistedKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	delisted := dvpn.Provider{Status: dvpn.ListingStatusDelisted}
	seedProviderAccount(t, fakeRPC, delistedKey.PublicKey(), delisted)

	fixture := newRunnerFixture(t, fakeRPC)

	cmd := cli.NewProviderSearchCmdForTest(&fixture.programClient.Client, fixture.printer.Printer)

	args := []string{"--status", "delisted"}
	cmd.SetArgs(args)

	require.NoError(t, cmd.Execute())

	var providers []struct {
		Address solana.PublicKey `json:"address"`
	}

	err = json.Unmarshal(fixture.printer.last, &providers)
	require.NoError(t, err)
	require.Len(t, providers, 1)
	require.Equal(t, delistedKey.PublicKey(), providers[0].Address)

	cmd = cli.NewProviderSearchCmdForTest(&fixture.programClient.Client, fixture.printer.Printer)

	args = []string{"--status", "listed"}
	cmd.SetArgs(args)

	require.NoError(t, cmd.Execute())

	err = json.Unmarshal(fixture.printer.last, &providers)
	require.NoError(t, err)
	require.Empty(t, providers)
}

func TestNewShowCmd_NodePrintsAddress(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	nodeAddress := solana.PublicKey{4}
	node := dvpn.Node{Status: dvpn.ListingStatusListed}
	fakeRPC.SetNode(dvpn.ProgramID, nodeAddress, node)
	fixture := newRunnerFixture(t, fakeRPC)
	cmd := cli.NewShowCmd(&fixture.programClient.Client, fixture.printer.Printer, "node", dvpn.FetchNode)
	cmd.SetArgs([]string{nodeAddress.String()})

	require.NoError(t, cmd.Execute())

	var got struct {
		Address solana.PublicKey `json:"address"`
	}

	err := json.Unmarshal(fixture.printer.last, &got)
	require.NoError(t, err)
	require.Equal(t, nodeAddress, got.Address)
}

func TestNewShowCmd_NodePrintsReadableFields(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	nodeAddress := solana.PublicKey{4}
	node := dvpn.Node{
		Country: [2]uint8{'D', 'E'},
		URILen:  11,
	}

	for i := range node.NodeID {
		node.NodeID[i] = 0xab
	}

	copy(node.URI[:], "https://a:1")
	fakeRPC.SetNode(dvpn.ProgramID, nodeAddress, node)
	fixture := newRunnerFixture(t, fakeRPC)
	cmd := cli.NewShowCmd(&fixture.programClient.Client, fixture.printer.Printer, "node", dvpn.FetchNode)
	cmd.SetArgs([]string{nodeAddress.String()})

	require.NoError(t, cmd.Execute())
	require.Contains(t, string(fixture.printer.last), `"uri": "https://a:1"`)
	require.Contains(t, string(fixture.printer.last), `"country": "DE"`)
	require.Contains(t, string(fixture.printer.last), `"node_id": "`+strings.Repeat("ab", 32)+`"`)
	require.NotContains(t, string(fixture.printer.last), `"uri_len"`)
	require.NotContains(t, string(fixture.printer.last), `"reserved"`)
}
