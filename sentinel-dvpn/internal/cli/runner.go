package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/spf13/cobra"

	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/key"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
	progcli "github.com/sentinel-official/sentinel-programs/clients/go/cli"
	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const (
	initializeConfigCmdName     = "initialize-config"
	initializeConfigInstruction = "initialize_config"
	sendingAnnotation           = "sending"
)

//nolint:gochecknoglobals
var providerOwnedCommands = map[string]struct{}{
	"close-provider":            {},
	"fund-leases":               {},
	"open-lease":                {},
	"withdraw-provider-balance": {},
	"withdraw-provider-revenue": {},
}

type resolved struct {
	owner           solana.PublicKey
	ownerToken      solana.PublicKey
	ownerTokenKnown bool
	rentPayer       solana.PublicKey
	signer          solana.PublicKey
	treasury        solana.PublicKey
	treasuryKnown   bool
}

var _ progcli.Runner = (*runner)(nil)

type runner struct {
	printer       *Printer
	programClient *program.Client
	resolved      resolved
	sender        *tx.Sender
	signer        *signerKey
}

func newRunner(programClient *program.Client, sender *tx.Sender, signer *signerKey, printer *Printer) *runner {
	return &runner{
		printer:       printer,
		programClient: programClient,
		sender:        sender,
		signer:        signer,
	}
}

func (r *runner) Account(name string) (solana.PublicKey, bool) {
	switch name {
	case "mint":
		return r.programClient.Mint, !r.programClient.Mint.IsZero()
	case "owner":
		return r.resolved.owner, !r.resolved.owner.IsZero()
	case "owner_token":
		return r.resolved.ownerToken, r.resolved.ownerTokenKnown
	case "program_data":
		address, err := core.FindProgramDataAddress(r.programClient.ProgramID)

		return address, err == nil && !r.programClient.ProgramID.IsZero()
	case "rent_payer":
		return r.resolved.rentPayer, !r.resolved.rentPayer.IsZero()
	case "token_program":
		return solana.Token2022ProgramID, true
	case "treasury":
		return r.resolved.treasury, r.resolved.treasuryKnown
	case "vault":
		address, err := r.programClient.VaultAddress()

		return address, err == nil && !r.programClient.Mint.IsZero()
	default:
		return solana.PublicKey{}, false
	}
}

func (r *runner) Options() core.Options {
	return r.programClient.Options()
}

func (r *runner) Print(v any) error {
	return r.printer.Print(v)
}

func (r *runner) ProgramID() solana.PublicKey {
	return r.programClient.ProgramID
}

func (r *runner) RPC() *rpc.Client {
	return r.programClient.RPC
}

func (r *runner) Run(ctx context.Context, name string, ix solana.Instruction) error {
	if name != initializeConfigInstruction {
		if err := r.programClient.CheckMint(ctx); err != nil {
			return err
		}
	}

	return sendAndPrint(ctx, r.sender, r.signer, r.printer, name, ix)
}

func (r *runner) Signer(ctx context.Context) (solana.PublicKey, error) {
	return r.signer.PublicKey(ctx)
}

func (r *runner) findTokenAccount(ctx context.Context, owner solana.PublicKey) (solana.PublicKey, bool) {
	ata, err := r.programClient.AssociatedTokenAddress(owner)
	if err != nil {
		return solana.PublicKey{}, false
	}

	opts := &rpc.GetAccountInfoOpts{Commitment: r.programClient.Commitment}

	resp, err := r.programClient.RPC.GetAccountInfoWithOpts(ctx, ata, opts)
	if err != nil || resp == nil || resp.Value == nil || resp.Value.Owner != solana.Token2022ProgramID {
		return solana.PublicKey{}, false
	}

	return ata, true
}

func (r *runner) prepare(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	r.resolved = resolved{}

	privateKey, err := r.signer.PrivateKey(ctx)
	if err != nil {
		return err
	}

	r.resolved.signer = privateKey.PublicKey()

	if cmd.Flags().Lookup("rent-payer") != nil {
		r.resolved.rentPayer, err = r.sender.RentPayer(ctx, r.resolved.signer)
		if err != nil {
			return fmt.Errorf("resolving rent payer: %w", err)
		}
	}

	ownerArg := readOwnerArg(cmd)

	owner, err := resolveOwner(ctx, r.signer, ownerArg)
	if err != nil {
		return err
	}

	_, providerOwned := providerOwnedCommands[cmd.Name()]

	providerFlag := cmd.Flags().Lookup("provider")
	if providerOwned && ownerArg == "" && providerFlag != nil && providerFlag.Changed {
		providerAddress, err := parseAddress("provider", providerFlag.Value.String())
		if err != nil {
			return err
		}

		provider, err := r.programClient.FetchProvider(ctx, providerAddress)
		if err != nil {
			return fmt.Errorf("fetching provider: %w", err)
		}

		owner = provider.Value.Owner
	}

	r.resolved.owner = owner

	if cmd.Name() != initializeConfigCmdName {
		account, err := r.programClient.FetchConfigBySeeds(ctx)
		if err != nil {
			err = fmt.Errorf("fetching config: %w", err)
			if !errors.Is(err, core.ErrAccountNotFound) {
				return err
			}
		} else {
			chainCfg := account.Value
			r.resolved.treasury = chainCfg.Treasury
			r.resolved.treasuryKnown = !chainCfg.Treasury.IsZero()
		}

		r.resolved.ownerToken, r.resolved.ownerTokenKnown = r.findTokenAccount(ctx, r.resolved.owner)
	}

	return nil
}

func (r *runner) prepareWith(name string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if err := r.prepare(cmd, args); err != nil {
			return err
		}

		if preRun, ok := preRunByCommand[name]; ok {
			return preRun(r, cmd)
		}

		return nil
	}
}

type sendResult struct {
	Events        []map[string]any `json:"events"`
	FeePayer      string           `json:"fee_payer"`
	LogsSimulated bool             `json:"logs_simulated"`
	Signature     string           `json:"signature"`
	Slot          uint64           `json:"slot"`
	TokenFee      *sendTokenFee    `json:"token_fee,omitempty"`
}

type sendTokenFee struct {
	BaseUnits uint64 `json:"base_units"`
	FeeToken  string `json:"fee_token"`
}

type signerKey struct {
	dir        string
	name       string
	privateKey solana.PrivateKey
}

func (k *signerKey) PrivateKey(_ context.Context) (solana.PrivateKey, error) {
	if k.privateKey != nil {
		return k.privateKey, nil
	}

	path, err := cli.KeyPath(k.dir, k.name)
	if err != nil {
		return nil, err
	}

	privateKey, err := key.Load(path, "")
	if errors.Is(err, key.ErrKeyLocked) {
		passphrase, passErr := cli.ReadPassphrase("passphrase for "+k.name, false)
		if passErr != nil {
			return nil, fmt.Errorf("reading passphrase: %w", passErr)
		}

		privateKey, err = key.Load(path, passphrase)
	}

	if err != nil {
		return nil, fmt.Errorf("loading key: %w", err)
	}

	k.privateKey = privateKey

	return privateKey, nil
}

func (k *signerKey) PublicKey(ctx context.Context) (solana.PublicKey, error) {
	privateKey, err := k.PrivateKey(ctx)
	if err != nil {
		return solana.PublicKey{}, err
	}

	return privateKey.PublicKey(), nil
}

func eventName(event any) string {
	name := fmt.Sprintf("%T", event)
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}

	return name
}

func isSending(cmd *cobra.Command) bool {
	return cmd.Annotations[sendingAnnotation] == "true"
}

func markSending(cmd *cobra.Command) {
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}

	cmd.Annotations[sendingAnnotation] = "true"
}

func readOwnerArg(cmd *cobra.Command) string {
	flag := cmd.Flags().Lookup("owner")
	if flag == nil || !flag.Changed {
		return ""
	}

	return flag.Value.String()
}

func sendAndPrint(
	ctx context.Context,
	sender *tx.Sender,
	signer *signerKey,
	printer *Printer,
	name string,
	ixs ...solana.Instruction,
) error {
	privateKey, err := signer.PrivateKey(ctx)
	if err != nil {
		return err
	}

	receipt, err := sender.Send(ctx, privateKey, ixs, tx.WithLabel(name))
	if err != nil {
		return err
	}

	return printer.Print(toSendResult(receipt))
}

func toSendResult(receipt *tx.Receipt) sendResult {
	result := sendResult{
		FeePayer:      receipt.FeePayer.String(),
		LogsSimulated: receipt.LogsSimulated,
		Signature:     receipt.Signature.String(),
		Slot:          receipt.Slot,
	}

	if receipt.TokenFee != nil {
		result.TokenFee = &sendTokenFee{
			BaseUnits: receipt.TokenFee.BaseUnits,
			FeeToken:  receipt.TokenFee.FeeToken.String(),
		}
	}

	for _, event := range dvpn.ParseEvents(receipt.Logs) {
		result.Events = append(result.Events, map[string]any{"event": event, "type": eventName(event)})
	}

	return result
}
