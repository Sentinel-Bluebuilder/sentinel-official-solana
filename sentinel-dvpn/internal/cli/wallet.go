package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/solana-foundation/solana-go/v2"
	associatedtokenaccount "github.com/solana-foundation/solana-go/v2/programs/associated-token-account"
	token2022 "github.com/solana-foundation/solana-go/v2/programs/token-2022"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/spf13/cobra"

	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

type balanceResult struct {
	DVPNBaseUnits uint64 `json:"dvpn_base_units"`
	Lamports      uint64 `json:"lamports"`
	Owner         string `json:"owner"`
}

func fetchTokenBalance(ctx context.Context, programClient *program.Client, owner solana.PublicKey) (uint64, error) {
	ata, err := programClient.AssociatedTokenAddress(owner)
	if err != nil {
		return 0, err
	}

	opts := rpc.GetAccountInfoOpts{
		Commitment: programClient.Commitment,
		Encoding:   solana.EncodingBase64,
	}

	resp, err := programClient.RPC.GetAccountInfoWithOpts(ctx, ata, &opts)
	if errors.Is(err, rpc.ErrNotFound) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("fetching token account: %w", err)
	}

	account, err := token2022.DecodeAccountWithExtensions(resp.Value.Data.GetBinary())
	if err != nil {
		return 0, fmt.Errorf("decoding token account: %w", err)
	}

	return account.Account.Amount, nil
}

func newWalletApproveDelegateCmd(
	programClient *program.Client,
	sender *tx.Sender,
	signer *signerKey,
	printer *Printer,
) *cobra.Command {
	var amount string

	run := func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()

		allowance, err := parseApproveAmount(amount)
		if err != nil {
			return err
		}

		if err := programClient.CheckMint(ctx); err != nil {
			return err
		}

		account, err := programClient.FetchConfigBySeeds(ctx)
		if err != nil {
			return fmt.Errorf("fetching config: %w", err)
		}

		chainCfg := account.Value

		signerAddress, err := signer.PublicKey(ctx)
		if err != nil {
			return err
		}

		delegate, _, err := dvpn.FindConfigAddress(programClient.ProgramID)
		if err != nil {
			return fmt.Errorf("deriving config address: %w", err)
		}

		ata, err := programClient.AssociatedTokenAddress(signerAddress)
		if err != nil {
			return err
		}

		instruction := token2022.NewApproveCheckedInstruction(
			allowance, chainCfg.Decimals, ata, programClient.Mint, delegate, signerAddress, nil,
		).Build()

		return sendAndPrint(ctx, sender, signer, printer, "approve_delegate", instruction)
	}
	cmd := &cobra.Command{
		Args:  cobra.NoArgs,
		RunE:  run,
		Short: "approve the program's delegate allowance on the wallet's dvpn token account",
		Use:   "approve-delegate",
	}
	cmd.Flags().StringVar(&amount, "amount-base-units", "", "`amount` in base units, or \"unlimited\"")
	markSending(cmd)

	return cmd
}

func newWalletBalanceCmd(programClient *program.Client, signer *signerKey, printer *Printer) *cobra.Command {
	var ownerArg string

	run := func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		if err := programClient.CheckMint(ctx); err != nil {
			return err
		}

		owner, err := resolveOwner(ctx, signer, ownerArg)
		if err != nil {
			return err
		}

		balanceLamports, err := programClient.RPC.GetBalance(ctx, owner, programClient.Commitment)
		if err != nil {
			return fmt.Errorf("fetching sol balance: %w", err)
		}

		balanceBaseUnits, err := fetchTokenBalance(ctx, programClient, owner)
		if err != nil {
			return err
		}

		out := balanceResult{
			DVPNBaseUnits: balanceBaseUnits,
			Lamports:      balanceLamports.Value,
			Owner:         owner.String(),
		}

		return printer.Print(out)
	}

	cmd := &cobra.Command{
		Args:  cobra.NoArgs,
		RunE:  run,
		Short: "show lamport and dvpn base-unit balances",
		Use:   "balance",
	}
	cmd.Flags().StringVar(&ownerArg, "owner", "", "wallet owner `address` (defaults to the signing key)")

	return cmd
}

func newWalletCmd(
	programClient *program.Client,
	sender *tx.Sender,
	signer *signerKey,
	printer *Printer,
) *cobra.Command {
	cmd := &cobra.Command{
		Short: "wallet operations",
		Use:   "wallet",
	}
	cmd.AddCommand(
		newWalletApproveDelegateCmd(programClient, sender, signer, printer),
		newWalletBalanceCmd(programClient, signer, printer),
		newWalletCreateTokenAccountCmd(programClient, sender, signer, printer),
		newWalletRevokeDelegateCmd(programClient, sender, signer, printer),
	)

	return cmd
}

func newWalletCreateTokenAccountCmd(
	programClient *program.Client,
	sender *tx.Sender,
	signer *signerKey,
	printer *Printer,
) *cobra.Command {
	run := func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		if err := programClient.CheckMint(ctx); err != nil {
			return err
		}

		signerAddress, err := signer.PublicKey(ctx)
		if err != nil {
			return err
		}

		rentPayer, err := sender.RentPayer(ctx, signerAddress)
		if err != nil {
			return fmt.Errorf("resolving rent payer: %w", err)
		}

		instruction := associatedtokenaccount.NewCreateIdempotentInstructionWithTokenProgram(
			rentPayer, signerAddress, programClient.Mint, solana.Token2022ProgramID,
		).Build()

		return sendAndPrint(ctx, sender, signer, printer, "create_token_account", instruction)
	}
	cmd := &cobra.Command{
		Args:  cobra.NoArgs,
		RunE:  run,
		Short: "create the wallet's dvpn token account",
		Use:   "create-token-account",
	}
	markSending(cmd)

	return cmd
}

func newWalletRevokeDelegateCmd(
	programClient *program.Client,
	sender *tx.Sender,
	signer *signerKey,
	printer *Printer,
) *cobra.Command {
	run := func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		if err := programClient.CheckMint(ctx); err != nil {
			return err
		}

		signerAddress, err := signer.PublicKey(ctx)
		if err != nil {
			return err
		}

		ata, err := programClient.AssociatedTokenAddress(signerAddress)
		if err != nil {
			return err
		}

		instruction := token2022.NewRevokeInstruction(ata, signerAddress, nil).Build()

		return sendAndPrint(ctx, sender, signer, printer, "revoke_delegate", instruction)
	}

	cmd := &cobra.Command{
		Args:  cobra.NoArgs,
		RunE:  run,
		Short: "revoke the program's delegate allowance on the wallet's dvpn token account",
		Use:   "revoke-delegate",
	}
	markSending(cmd)

	return cmd
}

func resolveOwner(ctx context.Context, signer *signerKey, ownerArg string) (solana.PublicKey, error) {
	if ownerArg != "" {
		return parseAddress("owner", ownerArg)
	}

	return signer.PublicKey(ctx)
}
