//go:build linux

package node

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/solana-foundation/solana-go/v2"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/providerapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const requestTimeout = 15 * time.Second

func (r *Runtime) admitTicket(ctx context.Context, signer solana.PublicKey, encoded string) (*admission, error) {
	signed, err := auth.DecodeTicket(encoded)
	if err != nil {
		return nil, api.ErrInvalidTicket
	}

	provider, err := r.programClient.FetchProvider(ctx, signed.Ticket.Provider)
	if err != nil {
		return nil, api.ErrInvalidTicket
	}

	if err := auth.VerifyTicket(ctx, r.programClient, signed, provider.Value.Owner, r.address, time.Now()); err != nil {
		return nil, api.ErrInvalidTicket
	}

	if err := auth.Authorize(
		ctx,
		r.programClient,
		signed.Ticket.User,
		signer,
		program.PermUseSubscription,
		signed.Ticket.Subscription,
		signed.Ticket.Provider,
	); err != nil {
		return nil, api.ErrUnauthorized
	}

	lease, err := r.findLease(ctx, signed.Ticket.Provider)
	if err != nil {
		return nil, err
	}

	digest := signed.Ticket.Hash()
	hash := hex.EncodeToString(digest[:])

	busy, err := r.hasActiveSession(ctx, hash)
	if err != nil {
		return nil, err
	}

	if busy {
		return nil, api.ErrPeerLimitReached
	}

	var stored Ticket

	consumedBytes := uint64(0)
	if err := r.db.WithContext(ctx).First(&stored, "hash = ?", hash).Error; err == nil {
		consumedBytes = stored.ConsumedBytes
	}

	if signed.Ticket.DataCapBytes != 0 && consumedBytes >= signed.Ticket.DataCapBytes {
		return nil, api.ErrInvalidTicket
	}

	ticket := Ticket{
		ConsumedBytes: consumedBytes,
		DataCapBytes:  signed.Ticket.DataCapBytes,
		ExpiresUnix:   signed.Ticket.ExpiresUnix,
		Hash:          hash,
		Lease:         lease.Address,
		LeaseHealthy:  true,
		Provider:      signed.Ticket.Provider.String(),
		Subscription:  signed.Ticket.Subscription.String(),
		User:          signed.Ticket.User.String(),
	}
	if err := r.db.WithContext(ctx).Save(&ticket).Error; err != nil {
		return nil, fmt.Errorf("saving ticket: %w", err)
	}

	return &admission{
		credential:  CredentialTicket,
		enforcer:    session.NewTicketEnforcer(signed.Ticket.DataCapBytes-consumedBytes, signed.Ticket.ExpiresUnix),
		expiresUnix: signed.Ticket.ExpiresUnix,
		signer:      signer.String(),
		subject:     hash,
		terms: session.Terms{
			Basis: session.BasisPerGB,
		},
		ticketHash: hash,
	}, nil
}

func (r *Runtime) checkTicketLeases(ctx context.Context) error {
	var tickets []Ticket
	if err := r.db.WithContext(ctx).Find(&tickets).Error; err != nil {
		return fmt.Errorf("loading tickets: %w", err)
	}

	for i := range tickets {
		ticket := &tickets[i]

		lease, err := solana.PublicKeyFromBase58(ticket.Lease)
		if err != nil {
			r.log.Warn().Err(err).Str("ticket", ticket.Hash).Msg("parsing ticket lease")

			continue
		}

		if _, err := r.refreshPosition(ctx, KindLease, lease); err != nil {
			r.log.Warn().Err(err).Str("lease", ticket.Lease).Msg("refreshing ticket lease")
		}
	}

	r.refreshLeaseHealth(ctx, time.Now().Unix())

	return nil
}

func (r *Runtime) findLease(ctx context.Context, provider solana.PublicKey) (*Position, error) {
	leases, err := dvpn.FindLeaseAccounts(
		ctx,
		r.programClient.RPC,
		r.programClient.ProgramID,
		r.programClient.Options(),
		dvpn.LeaseProviderFilter(provider),
		dvpn.LeaseNodeFilter(r.address),
	)
	if err != nil {
		return nil, fmt.Errorf("finding leases: %w", err)
	}

	_, node := r.chainState()
	now := time.Now().Unix()

	for _, keyed := range leases {
		if keyed.Value.Status != dvpn.PositionStatusOpen || keyed.Value.NodeOpenedAt != node.OpenedAt {
			continue
		}

		position, err := r.refreshPosition(ctx, KindLease, keyed.Address)
		if err != nil {
			return nil, err
		}

		if !r.leaseHealthy(position, node, now) {
			return nil, api.ErrLeaseNotFunded
		}

		return position, nil
	}

	return nil, api.ErrLeaseNotFunded
}

func (r *Runtime) forwardTicketRequest(ctx context.Context, encoded string) (string, error) {
	request, err := auth.DecodeTicketRequest(encoded)
	if err != nil {
		return "", api.ErrInvalidTicket
	}

	provider, err := r.programClient.FetchProvider(ctx, request.Request.Provider)
	if err != nil {
		return "", api.ErrInvalidTicket
	}

	metadata, uri, err := providerapi.Discover(
		ctx, r.programClient, request.Request.Provider, provider.Value, requestTimeout, api.WithDialContext(r.dial),
	)
	if err != nil {
		return "", api.ErrInvalidTicket
	}

	pinned, err := providerapi.NewPinnedClient(
		uri.String(), metadata.API.TLSSHA256, requestTimeout, api.WithDialContext(r.dial),
	)
	if err != nil {
		return "", api.ErrInternal
	}

	body := providerapi.IssueTicketJSONRequestBody{TicketRequest: encoded}

	issued, err := pinned.IssueTicketWithResponse(ctx, body)
	if err != nil || issued.JSON200 == nil {
		return "", api.ErrInvalidTicket
	}

	return issued.JSON200.Ticket, nil
}

func (r *Runtime) refreshLeaseHealth(ctx context.Context, now int64) {
	active := r.db.Model(&Session{}).Select("subject").
		Where("credential = ? AND status = ?", CredentialTicket, SessionStatusActive)

	var tickets []Ticket
	if err := r.db.WithContext(ctx).Where("hash IN (?)", active).Find(&tickets).Error; err != nil {
		r.log.Warn().Err(err).Msg("loading active tickets")

		return
	}

	_, node := r.chainState()

	for i := range tickets {
		ticket := &tickets[i]

		var position Position

		err := r.db.WithContext(ctx).First(&position, "address = ? AND kind = ?", ticket.Lease, KindLease).Error

		healthy := err == nil && r.leaseHealthy(&position, node, now)
		if healthy == ticket.LeaseHealthy {
			continue
		}

		if err := r.db.WithContext(ctx).Model(ticket).Update("lease_healthy", healthy).Error; err != nil {
			r.log.Warn().Err(err).Str("ticket", ticket.Hash).Msg("saving ticket health")
		}

		r.updateTicketEnforcer(ctx, ticket.Hash, healthy)
	}
}

func (r *Runtime) updateTicketEnforcer(ctx context.Context, hash string, healthy bool) {
	var sess Session
	if err := r.db.WithContext(ctx).
		Where("subject = ? AND status = ?", hash, SessionStatusActive).First(&sess).Error; err != nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if enforcer, ok := r.enforcers[sess.ID].(*session.TicketEnforcer); ok {
		enforcer.SetLeaseHealthy(healthy)
	}
}
