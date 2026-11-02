package cli_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpn/internal/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
	progcli "github.com/sentinel-official/sentinel-programs/clients/go/cli"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestNewChainGroups_ClosePositionResolvesNode(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"close-channel", "close-lease"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fakeRPC := sdktest.NewFakeRPC(t)
			fakeRPC.PrioritizationFees = []uint64{5_000}
			seedConfigAccount(t, fakeRPC, dvpn.Config{Mint: testMint})
			fixture := newRunnerFixture(t, fakeRPC)
			owner, err := fixture.runner.Signer(t.Context())
			require.NoError(t, err)
			node := randomPrivateKey(t).PublicKey()
			rentRecipient := randomPrivateKey(t).PublicKey()
			address := randomPrivateKey(t).PublicKey()
			flag := "channel"

			account := sdktest.Account{Owner: dvpn.ProgramID}
			if name == "close-channel" {
				account.Data = sdktest.EncodeAccount(dvpn.ChannelAccountDiscriminator, dvpn.Channel{
					Node:          node,
					Owner:         owner,
					RentRecipient: rentRecipient,
					Status:        dvpn.PositionStatusClosing,
				})
			} else {
				flag = "lease"
				account.Data = sdktest.EncodeAccount(dvpn.LeaseAccountDiscriminator, dvpn.Lease{
					Node:          node,
					Provider:      owner,
					RentRecipient: rentRecipient,
					Status:        dvpn.PositionStatusClosing,
				})
			}

			fakeRPC.SetAccount(address, account)

			cmd := findChainCmd(t, fixture.runner, flag, name)
			cmd.SetArgs([]string{"--" + flag, address.String()})
			require.NoError(t, cmd.Execute())

			sent := fakeRPC.Sent()
			require.Len(t, sent, 1)
			require.Contains(t, sent[0].Message.AccountKeys, node)
		})
	}
}

func TestNewChainGroups_FundChannelsAddsSubjectRemainingAccount(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.PrioritizationFees = []uint64{5_000}
	chainCfg := dvpn.Config{Mint: testMint}
	seedConfigAccount(t, fakeRPC, chainCfg)

	fixture := newRunnerFixture(t, fakeRPC)
	owner, err := fixture.runner.Signer(t.Context())
	require.NoError(t, err)

	subject, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	cmd := findChainCmd(t, fixture.runner, "channel", "fund-channels")

	ata, _, err := solana.FindAssociatedTokenAddressWithProgram(owner, testMint, solana.Token2022ProgramID)
	require.NoError(t, err)

	args := []string{
		"--owner", owner.String(),
		"--owner-token", ata.String(),
		"--amounts", "5",
		"--remaining-account", subject.PublicKey().String() + ":w",
	}
	cmd.SetArgs(args)

	require.NoError(t, cmd.Execute())

	sent := fakeRPC.Sent()
	require.Len(t, sent, 1)
	require.Contains(t, sent[0].Message.AccountKeys, subject.PublicKey())
}

func TestNewChainGroups_InitializeConfigReportsMissingFlagsOffline(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	rpcClient := rpc.New(server.URL)
	programClient := program.NewClient(rpcClient, dvpn.ProgramID, testMint, rpc.CommitmentConfirmed)
	sender := tx.NewSender(rpcClient, nil, tx.DefaultConfig(), zerolog.Nop())
	signer := cli.NewSignerKeyForTest(randomPrivateKey(t))
	runner := cli.NewRunner(programClient, sender, signer, nil)
	cmd := findChainCmd(t, runner, "program", "initialize-config")
	cmd.SetArgs(nil)

	err := cmd.Execute()
	require.ErrorIs(t, err, progcli.ErrMissingFlag)
	require.NotContains(t, err.Error(), "connection refused")
	require.Zero(t, requests.Load())
}

func TestNewChainGroups_OldOwnerTokenFlagRejected(t *testing.T) {
	t.Parallel()

	fixture := newRunnerFixture(t, sdktest.NewFakeRPC(t))
	cmd := findChainCmd(t, fixture.runner, "channel", "open-channel")

	address, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	cmd.SetArgs([]string{"--owner-token-account", address.PublicKey().String()})

	err = cmd.Execute()
	require.ErrorContains(t, err, "unknown flag: --owner-token-account")
}

func TestNewChainGroups_OpenGrantWildcardFlags(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		args        []string
		want        []string
		target      solana.PublicKey
		missingFlag bool
		wantErr     error
	}{
		{
			name: "wildcards",
			args: []string{"--any-target", "--never-expires", "--unlimited"},
			want: []string{"0", "18446744073709551615"},
		},
		{
			name:        "missing wildcards",
			missingFlag: true,
		},
		{
			name: "explicit values",
			args: []string{
				"--target", testMint.String(), "--expires-at", "123", "--allowance", "456",
				"--never-expires", "--unlimited",
			},
			want:   []string{"123", "456"},
			target: testMint,
		},
		{
			name:    "target with any-target",
			args:    []string{"--target", testMint.String(), "--any-target"},
			wantErr: cli.ErrTargetAmbiguous,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fakeRPC := sdktest.NewFakeRPC(t)
			fakeRPC.PrioritizationFees = []uint64{5_000}
			seedConfigAccount(t, fakeRPC, dvpn.Config{Mint: testMint})
			fixture := newRunnerFixture(t, fakeRPC)
			cmd := findChainCmd(t, fixture.runner, "grant", "open-grant")
			grantee := randomPrivateKey(t).PublicKey()
			cmd.SetArgs(append([]string{"--grantee", grantee.String(), "--permissions", "1"}, tc.args...))

			err := cmd.Execute()
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Empty(t, fakeRPC.Sent())

				return
			}

			if tc.missingFlag {
				require.ErrorIs(t, err, progcli.ErrMissingFlag)
				require.Empty(t, fakeRPC.Sent())

				return
			}

			require.NoError(t, err)

			sent := fakeRPC.Sent()
			require.Len(t, sent, 1)

			owner, err := fixture.runner.Signer(t.Context())
			require.NoError(t, err)
			grant, _, err := dvpn.FindGrantAddress(dvpn.ProgramID, owner, grantee, tc.target)
			require.NoError(t, err)
			require.Contains(t, sent[0].Message.AccountKeys, grant)
			require.True(t, cmd.Flags().Changed("target"))

			for i, name := range []string{"expires-at", "allowance"} {
				require.True(t, cmd.Flags().Changed(name), name)
				require.Equal(t, tc.want[i], cmd.Flags().Lookup(name).Value.String(), name)
			}
		})
	}
}

func TestNewChainGroups_OpenNodeSendsTransaction(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.PrioritizationFees = []uint64{5_000}
	chainCfg := dvpn.Config{Mint: testMint}
	seedConfigAccount(t, fakeRPC, chainCfg)

	fixture := newRunnerFixture(t, fakeRPC)

	signerAddress, err := fixture.runner.Signer(t.Context())
	require.NoError(t, err)

	ata, _, err := solana.FindAssociatedTokenAddressWithProgram(signerAddress, testMint, solana.Token2022ProgramID)
	require.NoError(t, err)
	fakeRPC.SetAccount(ata, sdktest.Account{Data: make([]byte, 165), Owner: solana.Token2022ProgramID})

	cmd := findChainCmd(t, fixture.runner, "node", "open-node")

	args := []string{
		"--node-id", strings.Repeat("11", 32),
		"--bond", "1",
		"--uri", "https://node.example.com",
		"--country", "DE",
		"--protocols", "1",
		"--prices.channel-price-per-gb", "1",
		"--prices.channel-price-per-hour", "1",
		"--prices.lease-price-per-hour", "1",
	}
	cmd.SetArgs(args)

	require.NoError(t, cmd.Execute())

	sent := fakeRPC.Sent()
	require.Len(t, sent, 1)

	ix := sent[0].Message.Instructions[0]
	require.Equal(t, dvpn.ProgramID, sent[0].Message.AccountKeys[ix.ProgramIDIndex])

	data := []byte(ix.Data)
	require.True(t, bytes.HasPrefix(data, dvpn.OpenNodeInstructionDiscriminator[:]))

	var receipt struct {
		Signature string `json:"signature"`
	}
	fixture.printer.decode(t, &receipt)
	require.NotEmpty(t, receipt.Signature)
}

func TestNewChainGroups_RegistersEveryGeneratedCommand(t *testing.T) {
	t.Parallel()

	fixture := newRunnerFixture(t, sdktest.NewFakeRPC(t))

	generated := map[string]bool{}
	for _, cmd := range dvpn.Commands(fixture.runner) {
		generated[cmd.Name()] = true
	}

	registered := map[string]bool{}

	for _, group := range cli.NewChainGroups(fixture.runner) {
		for _, cmd := range group.Commands() {
			registered[cmd.Name()] = true
		}
	}

	for name := range generated {
		if strings.HasPrefix(name, "show-") || strings.HasPrefix(name, "search-") {
			continue
		}

		require.True(t, registered[name], name)
	}
}

func TestNewChainGroups_WithdrawNodeBondErrorsWithoutUnbonding(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	chainCfg := dvpn.Config{Mint: testMint}
	seedConfigAccount(t, fakeRPC, chainCfg)

	fixture := newRunnerFixture(t, fakeRPC)

	cmd := findChainCmd(t, fixture.runner, "node", "withdraw-node-bond")
	cmd.SetArgs(nil)

	err := cmd.Execute()

	require.ErrorIs(t, err, progcli.ErrMissingFlag)
	require.Empty(t, fakeRPC.Sent())
}

func TestNewChainGroups_WithdrawUserBalanceDefaultsRentRecipient(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.PrioritizationFees = []uint64{5_000}
	seedConfigAccount(t, fakeRPC, dvpn.Config{Mint: testMint})
	fixture := newRunnerFixture(t, fakeRPC)
	owner, err := fixture.runner.Signer(t.Context())
	require.NoError(t, err)
	rentRecipient := randomPrivateKey(t).PublicKey()
	userAddress, _, err := dvpn.FindUserAddress(dvpn.ProgramID, owner)
	require.NoError(t, err)
	fakeRPC.SetAccount(userAddress, sdktest.Account{
		Data: sdktest.EncodeAccount(dvpn.UserAccountDiscriminator, dvpn.User{
			Owner:         owner,
			RentRecipient: rentRecipient,
		}),
		Owner: dvpn.ProgramID,
	})
	ownerToken, _, err := solana.FindAssociatedTokenAddressWithProgram(owner, testMint, solana.Token2022ProgramID)
	require.NoError(t, err)
	fakeRPC.SetAccount(ownerToken, sdktest.Account{Data: make([]byte, 165), Owner: solana.Token2022ProgramID})

	cmd := findChainCmd(t, fixture.runner, "user", "withdraw-user-balance")
	cmd.SetArgs(nil)
	require.NoError(t, cmd.Execute())

	sent := fakeRPC.Sent()
	require.Len(t, sent, 1)
	require.Contains(t, sent[0].Message.AccountKeys, rentRecipient)
}
