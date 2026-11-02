package cli_test

import (
	"math"
	"testing"

	"github.com/solana-foundation/solana-go/v2"
	associatedtokenaccount "github.com/solana-foundation/solana-go/v2/programs/associated-token-account"
	token2022 "github.com/solana-foundation/solana-go/v2/programs/token-2022"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpn/internal/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestNewWalletApproveDelegateCmd_UnlimitedEncodesMaxUint64(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.PrioritizationFees = []uint64{5_000}
	fixture := newRunnerFixture(t, fakeRPC)

	chainCfg := dvpn.Config{
		Decimals: 6,
		Mint:     testMint,
	}
	seedConfigAccount(t, fakeRPC, chainCfg)

	cmd := cli.NewWalletApproveDelegateCmd(fixture.programClient, fixture.sender, fixture.signer, fixture.printer.Printer)
	args := []string{"--amount-base-units", "unlimited"}
	cmd.SetArgs(args)

	require.NoError(t, cmd.ExecuteContext(t.Context()))

	sent := fakeRPC.Sent()
	require.Len(t, sent, 1)

	msg := sent[0].Message

	var approve *token2022.ApproveChecked

	for i := range msg.Instructions {
		ci := msg.Instructions[i]

		programID, err := msg.Program(ci.ProgramIDIndex)
		require.NoError(t, err)

		if programID != token2022.ProgramID {
			continue
		}

		accounts, err := ci.ResolveInstructionAccounts(&msg)
		require.NoError(t, err)

		inst, err := token2022.DecodeInstruction(accounts, ci.Data)
		require.NoError(t, err)

		approve, _ = inst.Impl.(*token2022.ApproveChecked)
	}

	require.NotNil(t, approve)
	require.NotNil(t, approve.Amount)
	require.Equal(t, uint64(math.MaxUint64), *approve.Amount)
}

func TestNewWalletBalanceCmd_RejectsMintMismatch(t *testing.T) {
	t.Parallel()

	sdktest.PinBuildIdentity(t, dvpn.ProgramID, testMint)
	fakeRPC := sdktest.NewFakeRPC(t)
	seedConfigAccount(t, fakeRPC, dvpn.Config{Mint: otherMint})

	cmd := cli.NewRootCmd()
	cmd.SetArgs([]string{
		"--home", t.TempDir(),
		"--rpc.url", fakeRPC.Config().URL,
		"--rpc.ws-url", fakeRPC.Config().WSURL,
		"wallet", "balance", "--owner", testMint.String(),
	})
	err := cmd.ExecuteContext(t.Context())

	require.ErrorIs(t, err, program.ErrMintMismatch)
}

func TestNewWalletCreateTokenAccountCmd_PaysRentThroughPaymaster(t *testing.T) {
	t.Parallel()

	paymasterKey := randomPrivateKey(t)

	cases := []struct {
		name      string
		paymaster tx.Paymaster
		want      solana.PublicKey
	}{
		{name: "self paid"},
		{
			name:      "paymaster",
			paymaster: sdktest.FakePaymaster{Key: paymasterKey},
			want:      paymasterKey.PublicKey(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fakeRPC := sdktest.NewFakeRPC(t)
			fakeRPC.PrioritizationFees = []uint64{5_000}
			fixture := newRunnerFixture(t, fakeRPC)
			fixture.sender.Paymaster = tc.paymaster

			seedConfigAccount(t, fakeRPC, dvpn.Config{Mint: testMint})

			signer, err := fixture.runner.Signer(t.Context())
			require.NoError(t, err)

			cmd := cli.NewWalletCreateTokenAccountCmd(
				fixture.programClient, fixture.sender, fixture.signer, fixture.printer.Printer,
			)
			cmd.SetArgs(nil)

			require.NoError(t, cmd.ExecuteContext(t.Context()))

			sent := fakeRPC.Sent()
			require.Len(t, sent, 1)

			want := tc.want
			if want.IsZero() {
				want = signer
			}

			var funder, wallet solana.PublicKey

			message := sent[0].Message
			for _, instruction := range message.Instructions {
				if message.AccountKeys[instruction.ProgramIDIndex] == associatedtokenaccount.ProgramID {
					funder = message.AccountKeys[instruction.Accounts[0]]
					wallet = message.AccountKeys[instruction.Accounts[2]]
				}
			}

			require.Equal(t, want, funder)
			require.Equal(t, signer, wallet)
		})
	}
}
