package client

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/solana-foundation/solana-go/v2"

	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
)

var ErrTicketOwnerMismatch = errors.New("ticket was issued for another owner")

var (
	errTicketNotRequested         = errors.New("node returned a ticket this session did not request")
	errTicketProviderMismatch     = errors.New("ticket was issued by another provider than requested")
	errTicketSubscriptionMismatch = errors.New("ticket was issued for another subscription than requested")
)

func (r *Runtime) signTicketRequest(
	request *daemonv1.TicketRequest,
	nodeAddress solana.PublicKey,
) (auth.SignedTicketRequest, error) {
	provider, err := solana.PublicKeyFromBase58(request.GetProvider())
	if err != nil {
		return auth.SignedTicketRequest{}, fmt.Errorf("provider: %w", err)
	}

	subscription, err := solana.PublicKeyFromBase58(request.GetSubscription())
	if err != nil {
		return auth.SignedTicketRequest{}, fmt.Errorf("subscription: %w", err)
	}

	nonce, err := auth.NewNonce()
	if err != nil {
		return auth.SignedTicketRequest{}, fmt.Errorf("generating nonce: %w", err)
	}

	req := auth.TicketRequest{
		Node:          nodeAddress,
		Nonce:         nonce,
		Provider:      provider,
		RequestedUnix: time.Now().Unix(),
		Subscription:  subscription,
		Version:       auth.TicketVersion,
	}

	return auth.SignTicketRequest(r.privateKey, req), nil
}

func (r *Runtime) verifyTicket(ctx context.Context, encoded string, requested *auth.TicketRequest) (*Ticket, error) {
	if requested == nil {
		return nil, errTicketNotRequested
	}

	signedTicket, err := auth.DecodeTicket(encoded)
	if err != nil {
		return nil, fmt.Errorf("decoding issued ticket: %w", err)
	}

	switch {
	case signedTicket.Ticket.User != r.owner:
		return nil, fmt.Errorf(
			"%w: ticket user %s, client owner %s", ErrTicketOwnerMismatch, signedTicket.Ticket.User, r.owner,
		)
	case signedTicket.Ticket.Provider != requested.Provider:
		return nil, fmt.Errorf(
			"%w: ticket provider %s, requested %s",
			errTicketProviderMismatch, signedTicket.Ticket.Provider, requested.Provider,
		)
	case signedTicket.Ticket.Subscription != requested.Subscription:
		return nil, fmt.Errorf(
			"%w: ticket subscription %s, requested %s",
			errTicketSubscriptionMismatch, signedTicket.Ticket.Subscription, requested.Subscription,
		)
	}

	providerAccount, err := r.programClient.FetchProvider(ctx, signedTicket.Ticket.Provider)
	if err != nil {
		return nil, fmt.Errorf("fetching provider account %s: %w", signedTicket.Ticket.Provider, err)
	}

	provider := providerAccount.Value

	if err := auth.VerifyTicket(
		ctx, r.programClient, signedTicket, provider.Owner, requested.Node, time.Now(),
	); err != nil {
		return nil, fmt.Errorf("verifying ticket: %w", err)
	}

	digest := signedTicket.Ticket.Hash()
	t := &Ticket{
		DataCapBytes: signedTicket.Ticket.DataCapBytes,

		Encoded:         encoded,
		ExpiresUnix:     signedTicket.Ticket.ExpiresUnix,
		Hash:            hex.EncodeToString(digest[:]),
		NodeAddress:     requested.Node.String(),
		ProviderAddress: signedTicket.Ticket.Provider.String(),

		SubscriptionAddress: signedTicket.Ticket.Subscription.String(),
	}

	return t, nil
}
