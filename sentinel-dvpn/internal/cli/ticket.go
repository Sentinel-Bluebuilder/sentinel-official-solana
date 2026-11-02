package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/spf13/cobra"

	"github.com/sentinel-official/sentinel-dvpn/internal/config"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/providerapi"
)

type ticketResult struct {
	Encoded string            `json:"encoded"`
	Ticket  auth.SignedTicket `json:"ticket"`
}

func newTicketCmd(
	programClient *program.Client,
	signer *signerKey,
	cfg *config.File,
	printer *Printer,
) *cobra.Command {
	cmd := &cobra.Command{
		Short: "request, show, and verify access tickets",
		Use:   "ticket",
	}
	cmd.AddCommand(
		newTicketRequestCmd(programClient, signer, cfg, printer),
		newTicketShowCmd(printer),
		newTicketVerifyCmd(programClient, printer),
	)

	return cmd
}

func newTicketRequestCmd(
	programClient *program.Client,
	signer *signerKey,
	cfg *config.File,
	printer *Printer,
) *cobra.Command {
	var (
		nodeArg         string
		providerArg     string
		subscriptionArg string
	)

	run := func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()

		providerAddress, err := parseAddress("provider", providerArg)
		if err != nil {
			return err
		}

		nodeAddress, err := parseAddress("node", nodeArg)
		if err != nil {
			return err
		}

		subscriptionAddress, err := parseAddress("subscription", subscriptionArg)
		if err != nil {
			return err
		}

		warning := "warning: ticket request reveals your ip address to the provider; " +
			"session start --ticket through the daemon does not"
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), warning); err != nil {
			return fmt.Errorf("writing warning: %w", err)
		}

		st, encoded, err := requestTicket(
			ctx,
			programClient,
			signer,
			providerAddress,
			nodeAddress,
			subscriptionAddress,
			cfg.Peer,
		)
		if err != nil {
			return err
		}

		return printer.Print(ticketResult{
			Encoded: encoded,
			Ticket:  st,
		})
	}

	cmd := &cobra.Command{
		Args:  cobra.NoArgs,
		RunE:  run,
		Short: "request an access ticket directly from a provider",
		Use:   "request",
	}
	cmd.Flags().StringVar(&nodeArg, "node", "", "node `address`")
	cmd.Flags().StringVar(&providerArg, "provider", "", "provider `address`")
	cmd.Flags().StringVar(&subscriptionArg, "subscription", "", "subscription `address`")
	_ = cmd.MarkFlagRequired("node")
	_ = cmd.MarkFlagRequired("provider")
	_ = cmd.MarkFlagRequired("subscription")

	return cmd
}

func newTicketShowCmd(printer *Printer) *cobra.Command {
	run := func(_ *cobra.Command, args []string) error {
		st, err := auth.DecodeTicket(args[0])
		if err != nil {
			return fmt.Errorf("decoding ticket: %w", err)
		}

		return printer.Print(st)
	}

	return &cobra.Command{
		Args:  cobra.ExactArgs(1),
		RunE:  run,
		Short: "decode and print an access ticket",
		Use:   "show <encoded>",
	}
}

func newTicketVerifyCmd(programClient *program.Client, printer *Printer) *cobra.Command {
	var nodeArg string

	run := func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		st, err := auth.DecodeTicket(args[0])
		if err != nil {
			return fmt.Errorf("decoding ticket: %w", err)
		}

		nodeAddress, err := parseAddress("node", nodeArg)
		if err != nil {
			return err
		}

		provider, err := programClient.FetchProvider(ctx, st.Ticket.Provider)
		if err != nil {
			return fmt.Errorf("fetching provider %s: %w", st.Ticket.Provider, err)
		}

		if err := auth.VerifyTicket(ctx, programClient, st, provider.Value.Owner, nodeAddress, time.Now()); err != nil {
			return fmt.Errorf("verifying ticket: %w", err)
		}

		return printer.Print(st)
	}

	cmd := &cobra.Command{
		Args:  cobra.ExactArgs(1),
		RunE:  run,
		Short: "verify an access ticket against the provider's on-chain owner",
		Use:   "verify <encoded>",
	}
	cmd.Flags().StringVar(&nodeArg, "node", "", "expected node `address`")
	_ = cmd.MarkFlagRequired("node")

	return cmd
}

func requestTicket(
	ctx context.Context,
	programClient *program.Client,
	signer *signerKey,
	providerAddress, nodeAddress, subscriptionAddress solana.PublicKey,
	peer cli.PeerConfig,
) (auth.SignedTicket, string, error) {
	provider, err := programClient.FetchProvider(ctx, providerAddress)
	if err != nil {
		return auth.SignedTicket{}, "", fmt.Errorf("fetching provider %s: %w", providerAddress, err)
	}

	dial := api.NewPeerDialer(net.DefaultResolver.LookupNetIP, peer.AllowPrivate)

	metadata, uri, err := providerapi.Discover(
		ctx,
		programClient,
		providerAddress,
		provider.Value,
		requestTimeout,
		api.WithDialContext(dial),
	)
	if err != nil {
		return auth.SignedTicket{}, "", err
	}

	privateKey, err := signer.PrivateKey(ctx)
	if err != nil {
		return auth.SignedTicket{}, "", err
	}

	nonce, err := auth.NewNonce()
	if err != nil {
		return auth.SignedTicket{}, "", fmt.Errorf("generating nonce: %w", err)
	}

	request := auth.TicketRequest{
		Node:          nodeAddress,
		Nonce:         nonce,
		Provider:      providerAddress,
		RequestedUnix: time.Now().Unix(),
		Subscription:  subscriptionAddress,
		Version:       auth.TicketVersion,
	}
	signedRequest := auth.SignTicketRequest(privateKey, request)

	pinned, err := providerapi.NewPinnedClient(
		uri.String(),
		metadata.API.TLSSHA256,
		requestTimeout,
		api.WithDialContext(dial),
	)
	if err != nil {
		return auth.SignedTicket{}, "", fmt.Errorf("dialing %s: %w", uri, err)
	}

	body := providerapi.IssueTicketJSONRequestBody{TicketRequest: signedRequest.Encode()}

	resp, err := pinned.IssueTicketWithResponse(ctx, body)
	if err != nil {
		return auth.SignedTicket{}, "", fmt.Errorf("requesting ticket from %s: %w", uri, err)
	}

	if resp.StatusCode() != http.StatusOK {
		return auth.SignedTicket{}, "", fmt.Errorf(
			"requesting ticket from %s: %w",
			uri,
			api.ParseError(resp.StatusCode(), resp.Body),
		)
	}

	if resp.JSON200 == nil {
		return auth.SignedTicket{}, "", fmt.Errorf("%w: %s: %s", errEmptyResponseBody, resp.Status(), resp.Body)
	}

	ticket, err := auth.DecodeTicket(resp.JSON200.Ticket)
	if err != nil {
		return auth.SignedTicket{}, "", fmt.Errorf("decoding ticket: %w", err)
	}

	if err := auth.VerifyTicket(ctx, programClient, ticket, provider.Value.Owner, nodeAddress, time.Now()); err != nil {
		return auth.SignedTicket{}, "", fmt.Errorf("verifying ticket: %w", err)
	}

	return ticket, resp.JSON200.Ticket, nil
}
