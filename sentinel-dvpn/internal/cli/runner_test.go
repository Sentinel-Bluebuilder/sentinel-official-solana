package cli_test

import (
	"errors"
	"testing"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/programs/system"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpn/internal/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

var errPaymasterUnreachable = errors.New("paymaster unreachable")

func TestRunner_Account_Defaults(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fixture := newRunnerFixture(t, fakeRPC)
	owner, err := fixture.runner.Signer(t.Context())
	require.NoError(t, err)

	treasury := solana.PublicKey{3}
	seedConfigAccount(t, fakeRPC, dvpn.Config{Mint: testMint, Treasury: treasury})

	cmd := &cobra.Command{}
	cmd.Flags().String("rent-payer", "", "")
	cmd.SetContext(t.Context())
	require.NoError(t, cli.Prepare(fixture.runner, cmd))
	mint, ok := fixture.runner.Account("mint")
	require.True(t, ok)
	require.Equal(t, testMint, mint)

	vault, ok := fixture.runner.Account("vault")
	require.True(t, ok)

	configAddress, _, err := dvpn.FindConfigAddress(dvpn.ProgramID)
	require.NoError(t, err)
	want, _, err := solana.FindAssociatedTokenAddressWithProgram(configAddress, testMint, solana.Token2022ProgramID)
	require.NoError(t, err)
	require.Equal(t, want, vault)

	rentPayer, ok := fixture.runner.Account("rent_payer")
	require.True(t, ok)
	require.Equal(t, owner, rentPayer)

	_, ok = fixture.runner.Account("owner_token")
	require.False(t, ok)

	pd, ok := fixture.runner.Account("program_data")
	require.True(t, ok)

	wantPD, err := core.FindProgramDataAddress(dvpn.ProgramID)
	require.NoError(t, err)
	require.Equal(t, wantPD, pd)

	got, ok := fixture.runner.Account("treasury")
	require.True(t, ok)
	require.Equal(t, treasury, got)

	defaultOwner, ok := fixture.runner.Account("owner")
	require.True(t, ok)
	require.Equal(t, owner, defaultOwner)

	wantToken, _, err := solana.FindAssociatedTokenAddressWithProgram(owner, testMint, solana.Token2022ProgramID)
	require.NoError(t, err)
	fakeRPC.SetAccount(wantToken, sdktest.Account{Data: make([]byte, 165), Owner: solana.Token2022ProgramID})

	require.NoError(t, cli.Prepare(fixture.runner, cmd))
	token, ok := fixture.runner.Account("owner_token")
	require.True(t, ok)
	require.Equal(t, wantToken, token)

	tp, ok := fixture.runner.Account("token_program")
	require.True(t, ok)
	require.Equal(t, solana.Token2022ProgramID, tp)
}

func TestRunner_Account_RentPayerFromPaymaster(t *testing.T) {
	t.Parallel()

	paymasterKey := randomPrivateKey(t)
	unreachable := sdktest.FakePaymaster{AddressErr: errPaymasterUnreachable}
	fallback := unreachable
	fallback.Fallback = true

	cases := []struct {
		name      string
		paymaster sdktest.FakePaymaster
		want      solana.PublicKey
		wantErr   error
	}{
		{
			name:      "paymaster address",
			paymaster: sdktest.FakePaymaster{Key: paymasterKey},
			want:      paymasterKey.PublicKey(),
		},
		{
			name:      "unreachable paymaster without fallback",
			paymaster: unreachable,
			wantErr:   errPaymasterUnreachable,
		},
		{
			name:      "unreachable paymaster with fallback",
			paymaster: fallback,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fakeRPC := sdktest.NewFakeRPC(t)
			fixture := newRunnerFixture(t, fakeRPC)
			fixture.sender.Paymaster = tc.paymaster

			seedConfigAccount(t, fakeRPC, dvpn.Config{Mint: testMint})

			signer, err := fixture.runner.Signer(t.Context())
			require.NoError(t, err)

			cmd := &cobra.Command{}
			cmd.Flags().String("rent-payer", "", "")
			cmd.SetContext(t.Context())

			err = cli.Prepare(fixture.runner, cmd)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)

				return
			}

			require.NoError(t, err)

			want := tc.want
			if want.IsZero() {
				want = signer
			}

			got, ok := fixture.runner.Account("rent_payer")
			require.True(t, ok)
			require.Equal(t, want, got)
		})
	}
}

func TestRunner_Prepare_KeepsSignerOwnerForSubscriptions(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fixture := newRunnerFixture(t, fakeRPC)
	seedConfigAccount(t, fakeRPC, dvpn.Config{Mint: testMint})

	signer, err := fixture.runner.Signer(t.Context())
	require.NoError(t, err)

	providerAddress := randomPrivateKey(t).PublicKey()
	seedProviderAccount(t, fakeRPC, providerAddress, dvpn.Provider{Owner: randomPrivateKey(t).PublicKey()})

	signerToken, _, err := solana.FindAssociatedTokenAddressWithProgram(signer, testMint, solana.Token2022ProgramID)
	require.NoError(t, err)
	fakeRPC.SetAccount(signerToken, sdktest.Account{
		Data:  make([]byte, 165),
		Owner: solana.Token2022ProgramID,
	})

	cmd := &cobra.Command{Use: "open-subscription"}
	cmd.SetContext(t.Context())
	cmd.Flags().String("provider", "", "")
	require.NoError(t, cmd.Flags().Set("provider", providerAddress.String()))

	require.NoError(t, cli.Prepare(fixture.runner, cmd))
	owner, ok := fixture.runner.Account("owner")
	require.True(t, ok)
	require.Equal(t, signer, owner)

	token, ok := fixture.runner.Account("owner_token")
	require.True(t, ok)
	require.Equal(t, signerToken, token)
}

func TestRunner_Prepare_SkipsRentPayerWithoutFlag(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fixture := newRunnerFixture(t, fakeRPC)
	fixture.sender.Paymaster = sdktest.FakePaymaster{AddressErr: errPaymasterUnreachable}

	seedConfigAccount(t, fakeRPC, dvpn.Config{Mint: testMint})

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())

	require.NoError(t, cli.Prepare(fixture.runner, cmd))

	_, ok := fixture.runner.Account("rent_payer")
	require.False(t, ok)
}

func TestRunner_Prepare_UsesProviderOwnerForToken(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fixture := newRunnerFixture(t, fakeRPC)
	seedConfigAccount(t, fakeRPC, dvpn.Config{Mint: testMint})

	providerAddress := randomPrivateKey(t).PublicKey()
	providerOwner := randomPrivateKey(t).PublicKey()
	seedProviderAccount(t, fakeRPC, providerAddress, dvpn.Provider{Owner: providerOwner})

	providerToken, _, err := solana.FindAssociatedTokenAddressWithProgram(
		providerOwner, testMint, solana.Token2022ProgramID,
	)
	require.NoError(t, err)
	fakeRPC.SetAccount(providerToken, sdktest.Account{
		Data:  make([]byte, 165),
		Owner: solana.Token2022ProgramID,
	})

	cmd := &cobra.Command{Use: "open-lease"}
	cmd.SetContext(t.Context())
	cmd.Flags().String("provider", "", "")
	require.NoError(t, cmd.Flags().Set("provider", providerAddress.String()))

	require.NoError(t, cli.Prepare(fixture.runner, cmd))
	owner, ok := fixture.runner.Account("owner")
	require.True(t, ok)
	require.Equal(t, providerOwner, owner)

	token, ok := fixture.runner.Account("owner_token")
	require.True(t, ok)
	require.Equal(t, providerToken, token)

	explicitOwner := randomPrivateKey(t).PublicKey()
	explicitToken, _, err := solana.FindAssociatedTokenAddressWithProgram(
		explicitOwner, testMint, solana.Token2022ProgramID,
	)
	require.NoError(t, err)
	fakeRPC.SetAccount(explicitToken, sdktest.Account{
		Data:  make([]byte, 165),
		Owner: solana.Token2022ProgramID,
	})
	cmd.Flags().String("owner", "", "")
	require.NoError(t, cmd.Flags().Set("owner", explicitOwner.String()))

	require.NoError(t, cli.Prepare(fixture.runner, cmd))
	owner, ok = fixture.runner.Account("owner")
	require.True(t, ok)
	require.Equal(t, explicitOwner, owner)

	token, ok = fixture.runner.Account("owner_token")
	require.True(t, ok)
	require.Equal(t, explicitToken, token)
}

func TestRunner_Run_PrintsSendResult(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		logsSimulated bool
	}{
		{name: "landed logs"},
		{name: "simulation fallback", logsSimulated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fakeRPC := sdktest.NewFakeRPC(t)
			fakeRPC.PrioritizationFees = []uint64{5_000}
			fakeRPC.TransactionRPCError = tc.logsSimulated
			fixture := newRunnerFixture(t, fakeRPC)
			payer, err := fixture.runner.Signer(t.Context())
			require.NoError(t, err)
			seedConfigAccount(t, fakeRPC, dvpn.Config{Mint: testMint})

			ix := system.NewTransferInstruction(1, payer, payer).Build()

			require.NoError(t, fixture.runner.Run(t.Context(), "transfer", ix))

			var receipt struct {
				FeePayer      string `json:"fee_payer"`
				LogsSimulated bool   `json:"logs_simulated"`
				Signature     string `json:"signature"`
				Slot          uint64 `json:"slot"`
			}
			fixture.printer.decode(t, &receipt)
			require.Equal(t, payer.String(), receipt.FeePayer)
			require.Equal(t, tc.logsSimulated, receipt.LogsSimulated)
			require.NotEmpty(t, receipt.Signature)
			require.EqualValues(t, 100, receipt.Slot)
			require.Len(t, fakeRPC.Sent(), 1)
		})
	}
}

func TestRunner_Run_RejectsMintMismatch(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fixture := newRunnerFixture(t, fakeRPC)
	seedConfigAccount(t, fakeRPC, dvpn.Config{Mint: otherMint})

	payer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	ix := system.NewTransferInstruction(1, payer.PublicKey(), payer.PublicKey()).Build()
	err = fixture.runner.Run(t.Context(), "transfer", ix)

	require.ErrorIs(t, err, program.ErrMintMismatch)
	require.Empty(t, fakeRPC.Sent())
}

func TestToSendResult_TokenFee(t *testing.T) {
	t.Parallel()

	feePayer := solana.PublicKey{7}
	feeToken := solana.PublicKey{8}

	cases := []struct {
		name     string
		tokenFee *tx.TokenFee
		want     map[string]any
	}{
		{name: "no token fee"},
		{
			name:     "token fee paid",
			tokenFee: &tx.TokenFee{BaseUnits: 1_250, FeeToken: feeToken},
			want:     map[string]any{"base_units": float64(1_250), "fee_token": feeToken.String()},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			printer := newRecordingPrinter()
			require.NoError(t, printer.Print(cli.ToSendResult(&tx.Receipt{FeePayer: feePayer, TokenFee: tc.tokenFee})))

			var result map[string]any
			printer.decode(t, &result)
			require.Equal(t, feePayer.String(), result["fee_payer"])

			if tc.want == nil {
				require.NotContains(t, result, "token_fee")

				return
			}

			require.Equal(t, tc.want, result["token_fee"])
		})
	}
}
