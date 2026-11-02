//go:build linux

package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"gorm.io/gorm"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
	"github.com/sentinel-official/sentinel-go-sdk/v3/voucher"
)

const (
	maxStreamBackoff = 10 * time.Second
	minStreamBackoff = time.Second
	missedHeartbeats = 3
	pollInterval     = 5 * time.Second
	sampleInterval   = time.Second
)

var (
	errLeaseNotOpen = errors.New("lease is not open")
	errNodeChanged  = errors.New("lease node epoch changed")
)

type leaseEvent struct {
	err   error
	event nodeapi.Event
}

type leasePayment struct {
	block          uint64
	lease          Lease
	minimumSeconds uint64
	node           session.Usage
	opening        bool
	paid           uint64
	payer          *session.Payer
	pinned         bool
	price          uint64
}

type leaseWatcher struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (r *Runtime) auditLeasePayment(payment *leasePayment, view nodeapi.Position, snapshot Lease) error {
	ledgerLease := payment.lease
	if !leaseLedgerMatches(ledgerLease, view.Ledger) && leaseLedgerMatches(snapshot, view.Ledger) {
		ledgerLease = snapshot
	}

	if err := r.verifyLeaseLedger(ledgerLease, view, payment.opening); err != nil {
		return err
	}

	if payment.pinned && view.Ledger.Price != payment.price {
		return session.ErrNodePriceChanged
	}

	payment.price = view.Ledger.Price
	payment.pinned = true
	payment.paid = max(payment.paid, view.Ledger.CumulativeBaseUnits)

	terms := session.Terms{
		Basis: session.BasisPerHour,
		Price: payment.price,
	}

	block, err := session.PrepayBlock(
		terms,
		r.cfg.Lease.PrepaySeconds,
		payment.minimumSeconds,
		r.cfg.Lease.MaxPrepaySeconds,
	)
	if err != nil {
		return err
	}

	payment.block = block
	payment.payer = session.NewPayer(terms, payment.block, payment.lease.Escrow, r.cfg.Lease.TolerancePercent)
	payment.payer.Resume(payment.paid, 0)

	local := leaseLocalUsage(payment.lease, time.Now())
	if err := payment.payer.Audit(local, view.Consumption.Usage,
		view.Consumption.SettledBaseUnits, view.Consumption.OwedBaseUnits); err != nil {
		return fmt.Errorf("auditing lease usage: %w", err)
	}

	if payment.opening {
		owed := max(view.Consumption.OwedBaseUnits,
			session.Owed(session.BasisPerHour, payment.price, local.Seconds))

		cumulative, err := payment.payer.Opening(owed, 0)
		if errors.Is(err, session.ErrPrepayZero) || errors.Is(err, session.ErrPrepayTooSmall) {
			return err
		}

		if err != nil || (payment.price > 0 && cumulative <= owed) {
			return errors.Join(session.ErrOwedExceedsPaid, err)
		}
	}

	payment.node = view.Consumption.Usage

	return nil
}

func (r *Runtime) handleLeaseViolation(ctx context.Context, lease Lease, probe Probe, err error) {
	r.log.Error().Err(err).Str("lease", lease.Address).Msg("lease ledger violation")

	if !errors.Is(err, errNodeChanged) {
		now := time.Now().Unix()

		if err := r.db.WithContext(ctx).Transaction(func(db *gorm.DB) error {
			if err := db.Delete(&RosterNode{}, "node = ?", probe.Node).Error; err != nil {
				return err
			}

			return db.Model(&Probe{}).Where("node = ?", probe.Node).
				Updates(map[string]any{"live": false, "dead_since_unix": now}).Error
		}); err != nil {
			r.log.Error().Err(err).Str("node", probe.Node).Msg("excluding violated node failed")
		}
	}

	closeCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	if err := r.startCloseLease(closeCtx, &lease); err != nil {
		r.log.Error().Err(err).Str("lease", lease.Address).Msg("closing lease failed")
	}
}

func (r *Runtime) payLease(
	ctx context.Context,
	client *nodeapi.ClientWithResponses,
	payment *leasePayment,
) (bool, error) {
	snapshot := payment.lease

	cumulative, issue := payment.payer.Next(leaseLocalUsage(snapshot, time.Now()), payment.node)
	if !issue {
		return false, nil
	}

	address := solana.MustPublicKeyFromBase58(payment.lease.Address)
	signed := voucher.Sign(r.privateKey, voucher.LeaseMessage(address, payment.lease.OpenedUnix, cumulative))

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	resp, err := client.SendLeaseVoucherWithResponse(ctx, address, nodeapi.SendVoucherRequest{
		Voucher: nodeapi.Voucher{
			Cumulative: cumulative,
			Signature:  signed.Signature[:],
		},
	})
	if err != nil {
		return false, fmt.Errorf("sending lease voucher: %w", err)
	}

	if resp.JSON200 == nil {
		return false, fmt.Errorf("sending lease voucher: %w", api.ParseError(resp.StatusCode(), resp.Body))
	}

	payment.opening = false
	payment.paid = max(payment.paid, cumulative)
	payment.payer.RecordIssued(cumulative)
	r.metrics.vouchers.WithLabelValues(kindLease, resultAccepted).Inc()

	if err := r.db.WithContext(ctx).Model(&Lease{}).
		Where(
			"address = ? AND opened_unix = ? AND cumulative <= ?",
			payment.lease.Address,
			payment.lease.OpenedUnix,
			cumulative,
		).
		Updates(map[string]any{
			"cumulative": cumulative, "signature": signed.Signature[:], "updated_unix": time.Now().Unix(),
		}).Error; err != nil {
		return true, fmt.Errorf("saving lease voucher: %w", err)
	}

	active, err := r.refreshLeasePayment(ctx, payment)
	if err != nil {
		return true, err
	}

	if !active {
		return true, errLeaseNotOpen
	}

	if err := r.auditLeasePayment(payment, *resp.JSON200, snapshot); err != nil {
		return true, err
	}

	return true, nil
}

func (r *Runtime) readLease(
	ctx context.Context,
	client *nodeapi.ClientWithResponses,
	lease Lease,
) (*nodeapi.Position, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	resp, err := client.GetLeaseWithResponse(ctx, solana.MustPublicKeyFromBase58(lease.Address))
	if err != nil {
		return nil, fmt.Errorf("fetching lease ledger: %w", err)
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("fetching lease ledger: %w", api.ParseError(resp.StatusCode(), resp.Body))
	}

	return resp.JSON200, nil
}

func (r *Runtime) readLeaseEvents(
	ctx context.Context,
	client *nodeapi.ClientWithResponses,
	lease Lease,
	events chan<- leaseEvent,
	heartbeat time.Duration,
) {
	backoff := minStreamBackoff

	for ctx.Err() == nil {
		received, err := r.readLeaseStream(ctx, client, lease, events, heartbeat)
		if err != nil {
			select {
			case <-ctx.Done():
			case events <- leaseEvent{err: err}:
			}

			return
		}

		if received {
			backoff = minStreamBackoff
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()

			return
		case <-timer.C:
		}

		backoff = min(backoff*2, maxStreamBackoff)
	}
}

func (r *Runtime) readLeaseStream(
	ctx context.Context,
	client *nodeapi.ClientWithResponses,
	lease Lease,
	events chan<- leaseEvent,
	heartbeat time.Duration,
) (bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	timeout := heartbeat * missedHeartbeats

	watchdog := time.AfterFunc(timeout, cancel)
	defer watchdog.Stop()

	resp, err := client.GetLeaseEvents(ctx, solana.MustPublicKeyFromBase58(lease.Address))
	if isInvalidResponse(err) {
		return false, err
	}

	if err != nil {
		return false, nil
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return false, nil
	}

	decoder := api.NewEventDecoder(resp.Body)
	received := false

	for {
		frame, err := decoder.Next()
		if err != nil {
			return received, nil
		}

		event, err := nodeapi.DecodeEvent(frame)
		if err != nil {
			return received, err
		}

		received = true

		watchdog.Reset(timeout)

		select {
		case <-ctx.Done():
			return received, nil
		case events <- leaseEvent{event: event}:
		}
	}
}

func (r *Runtime) refreshLeasePayment(ctx context.Context, payment *leasePayment) (bool, error) {
	var lease Lease
	if err := r.db.WithContext(ctx).First(&lease, "address = ?", payment.lease.Address).Error; err != nil {
		return false, fmt.Errorf("reading lease: %w", err)
	}

	if lease.OpenedUnix != payment.lease.OpenedUnix || lease.NodeOpenedUnix != payment.lease.NodeOpenedUnix ||
		lease.Status != StatusOpen {
		return false, nil
	}

	payment.lease = lease
	payment.paid = max(payment.paid, lease.Cumulative, lease.Redeemed)
	payment.payer = session.NewPayer(session.Terms{
		Basis: session.BasisPerHour,
		Price: payment.price,
	},
		payment.block, lease.Escrow, r.cfg.Lease.TolerancePercent)
	payment.payer.Resume(payment.paid, 0)

	return true, nil
}

func (r *Runtime) runLeaseWatcher(ctx context.Context, lease Lease, probe Probe) {
	if err := r.verifyLeaseNode(ctx, lease); err != nil {
		if errors.Is(err, errNodeChanged) {
			r.handleLeaseViolation(ctx, lease, probe, err)
		} else {
			r.log.Warn().Err(err).Str("lease", lease.Address).Msg("checking lease node failed")
		}

		return
	}

	if probe.MinLeasePrepaySeconds > r.cfg.Lease.MaxPrepaySeconds {
		r.log.Error().Str("lease", lease.Address).Err(session.ErrPrepayExceedsMaximum).Msg("invalid lease prepay minimum")

		return
	}

	client, err := nodeapi.NewPinnedClient(probe.APIURL, probe.TLSSHA256, r.privateKey, 0, api.WithDialContext(r.dial),
		func(transport *http.Transport) { transport.ResponseHeaderTimeout = requestTimeout })
	if err != nil {
		r.log.Error().Err(err).Str("lease", lease.Address).Msg("building lease client failed")

		return
	}

	payment := &leasePayment{
		lease:          lease,
		minimumSeconds: probe.MinLeasePrepaySeconds,
		opening:        true,
		paid:           lease.Cumulative,
		price:          lease.RatePerHour,
	}

	view, err := r.readLease(ctx, client, lease)
	lastContact := time.Now()

	if isLeaseViolation(err) {
		r.handleLeaseViolation(ctx, lease, probe, err)

		return
	}

	if err != nil {
		r.log.Warn().Err(err).Str("lease", lease.Address).Msg("fetching lease failed")

		return
	}

	active, err := r.refreshLeasePayment(ctx, payment)
	if err != nil {
		r.log.Error().Err(err).Str("lease", payment.lease.Address).Msg("refreshing lease failed")

		return
	}

	if !active {
		return
	}

	if err := r.auditLeasePayment(payment, *view, payment.lease); err != nil {
		if errors.Is(err, session.ErrPrepayZero) || errors.Is(err, session.ErrPrepayTooSmall) {
			r.log.Error().Err(err).Str("lease", lease.Address).Msg("invalid lease prepay block")
		} else {
			r.handleLeaseViolation(ctx, payment.lease, probe, err)
		}

		return
	}

	contacted, err := r.payLease(ctx, client, payment)
	if contacted {
		lastContact = time.Now()
	}

	if errors.Is(err, errLeaseNotOpen) {
		return
	}

	if isLeaseViolation(err) {
		r.handleLeaseViolation(ctx, payment.lease, probe, err)

		return
	}

	if err != nil {
		r.log.Warn().Err(err).Str("lease", payment.lease.Address).Msg("paying lease failed")
	} else {
		payment.opening = false
	}

	r.watchLeasePayments(ctx, client, payment, probe, lastContact)
}

func (r *Runtime) startLeaseWatcher(ctx context.Context, lease Lease, probe Probe) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.stopping || r.watchers[lease.Address] != nil {
		return
	}

	lifecycle, cancel := context.WithCancel(context.WithoutCancel(ctx))
	watcher := &leaseWatcher{
		cancel: cancel,
		done:   make(chan struct{}),
	}
	r.watchers[lease.Address] = watcher

	go func() {
		defer cancel()
		defer func() {
			r.mu.Lock()
			delete(r.watchers, lease.Address)
			close(watcher.done)
			r.mu.Unlock()
		}()

		r.runLeaseWatcher(lifecycle, lease, probe)
	}()
}

func (r *Runtime) stopLeaseWatcher(address string) {
	r.mu.Lock()
	watcher := r.watchers[address]
	r.mu.Unlock()

	if watcher == nil {
		return
	}

	watcher.cancel()
	<-watcher.done
}

func (r *Runtime) stopLeaseWatchers() {
	r.mu.Lock()
	r.stopping = true

	watchers := make([]*leaseWatcher, 0, len(r.watchers))
	for _, watcher := range r.watchers {
		watcher.cancel()
		watchers = append(watchers, watcher)
	}
	r.mu.Unlock()

	for _, watcher := range watchers {
		<-watcher.done
	}
}

func (r *Runtime) verifyLeaseLedger(lease Lease, view nodeapi.Position, opening bool) error {
	if view.Ledger.Status != nodeapi.LedgerStatusOpen {
		return session.ErrLedgerMismatch
	}

	if !leaseLedgerMatches(lease, view.Ledger) {
		return session.ErrLedgerMismatch
	}

	if view.Ledger.CumulativeBaseUnits > view.Ledger.RedeemedBaseUnits {
		address, err := solana.PublicKeyFromBase58(lease.Address)
		if err != nil {
			return fmt.Errorf("%w: %w", session.ErrInvalidLedger, err)
		}

		message := voucher.LeaseMessage(address, lease.OpenedUnix, view.Ledger.CumulativeBaseUnits)
		if err := voucher.Verify(r.signer, message, view.Ledger.Signature); err != nil {
			return fmt.Errorf("%w: %w", session.ErrInvalidLedger, err)
		}
	}

	if view.Ledger.CumulativeBaseUnits > lease.Escrow || view.Ledger.RedeemedBaseUnits > view.Ledger.CumulativeBaseUnits {
		return session.ErrLedgerMismatch
	}

	if !opening && view.Consumption.OwedBaseUnits > view.Ledger.CumulativeBaseUnits {
		return session.ErrOwedExceedsPaid
	}

	if view.Consumption.SettledBaseUnits != 0 {
		return session.ErrSettledExceedsBound
	}

	if view.Ledger.Basis != session.BasisPerHour || view.Ledger.Price > lease.RatePerHour {
		return session.ErrNodePriceChanged
	}

	return nil
}

func (r *Runtime) verifyLeaseNode(ctx context.Context, lease Lease) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	address, err := solana.PublicKeyFromBase58(lease.Node)
	if err != nil {
		return fmt.Errorf("parsing lease node: %w", err)
	}

	node, err := r.programClient.FetchNode(ctx, address)
	if err != nil {
		return fmt.Errorf("fetching lease node: %w", err)
	}

	if node.Value.OpenedAt != lease.NodeOpenedUnix {
		return errNodeChanged
	}

	return nil
}

func (r *Runtime) watchLeasePayments(
	ctx context.Context,
	client *nodeapi.ClientWithResponses,
	payment *leasePayment,
	probe Probe,
	lastContact time.Time,
) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	events := make(chan leaseEvent)
	done := make(chan struct{})
	lease := payment.lease

	go func() {
		defer close(done)

		r.readLeaseEvents(ctx, client, lease, events, time.Duration(max(probe.HeartbeatSeconds, 1))*time.Second)
	}()

	defer func() { cancel(); <-done }()

	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()

	now := time.Now()
	lastStream, lastPoll := now, now

	for {
		select {
		case <-ctx.Done():
			return
		case received := <-events:
			if received.err != nil {
				r.handleLeaseViolation(ctx, payment.lease, probe, received.err)

				return
			}

			now = time.Now()
			lastContact, lastStream = now, now

			if received.event.Closed != nil {
				return
			}

			if request := received.event.VoucherRequest; request != nil {
				snapshot := payment.lease
				view := nodeapi.Position(*request)

				active, err := r.refreshLeasePayment(ctx, payment)
				if err != nil {
					r.log.Error().Err(err).Str("lease", payment.lease.Address).Msg("refreshing lease failed")

					return
				}

				if !active {
					return
				}

				if err := r.auditLeasePayment(payment, view, snapshot); err != nil {
					r.handleLeaseViolation(ctx, payment.lease, probe, err)

					return
				}
			}
		case <-ticker.C:
			now = time.Now()
			contactExpired := now.Sub(lastContact) > time.Duration(r.cfg.Lease.NodeTimeoutSeconds)*time.Second
			streamExpired := now.Sub(lastStream) >=
				time.Duration(max(probe.HeartbeatSeconds, 1))*time.Second*missedHeartbeats

			if (contactExpired || streamExpired) && now.Sub(lastPoll) >= pollInterval {
				lastPoll = now

				snapshot := payment.lease

				view, err := r.readLease(ctx, client, snapshot)
				if isLeaseViolation(err) {
					r.handleLeaseViolation(ctx, payment.lease, probe, err)

					return
				}

				if err == nil {
					lastContact = time.Now()

					active, err := r.refreshLeasePayment(ctx, payment)
					if err != nil {
						r.log.Error().Err(err).Str("lease", payment.lease.Address).Msg("refreshing lease failed")

						return
					}

					if !active {
						return
					}

					if err := r.auditLeasePayment(payment, *view, snapshot); err != nil {
						r.handleLeaseViolation(ctx, payment.lease, probe, err)

						return
					}
				}
			}
		}

		active, err := r.refreshLeasePayment(ctx, payment)
		if err != nil {
			r.log.Error().Err(err).Str("lease", payment.lease.Address).Msg("refreshing lease failed")

			return
		}

		if !active {
			return
		}

		contacted, err := r.payLease(ctx, client, payment)
		if contacted {
			lastContact = time.Now()
		}

		if errors.Is(err, errLeaseNotOpen) {
			return
		}

		if err != nil {
			if isLeaseViolation(err) {
				r.handleLeaseViolation(ctx, payment.lease, probe, err)

				return
			}

			r.log.Warn().Err(err).Str("lease", payment.lease.Address).Msg("paying lease failed")
		}
	}
}

func isInvalidResponse(err error) bool {
	return errors.Is(err, api.ErrInvalidResponse) || errors.Is(err, api.ErrResponseTooLarge) ||
		errors.Is(err, api.ErrUnexpectedRedirect)
}

func isLeaseViolation(err error) bool {
	return errors.Is(err, errNodeChanged) || errors.Is(err, session.ErrInvalidLedger) ||
		errors.Is(err, session.ErrLedgerMismatch) ||
		errors.Is(err, session.ErrOwedExceedsPaid) ||
		errors.Is(err, session.ErrNodePriceChanged) || errors.Is(err, session.ErrSettledExceedsBound) ||
		errors.Is(err, session.ErrUsageMismatch) ||
		errors.Is(err, session.ErrOwedMismatch) || errors.Is(err, session.ErrSettledChanged) ||
		isInvalidResponse(err)
}

func leaseLedgerMatches(lease Lease, ledger nodeapi.Ledger) bool {
	return ledger.EscrowBaseUnits == lease.Escrow && ledger.RedeemedBaseUnits == lease.Redeemed &&
		ledger.OpenedAt == lease.OpenedUnix
}

func leaseLocalUsage(lease Lease, now time.Time) session.Usage {
	return session.Usage{Seconds: uint64(max(now.Unix()-lease.OpenedUnix, 0))}
}
