//go:build linux

package node

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

var errMissingMeter = errors.New("active session has no meter")

func (r *Runtime) positionView(ctx context.Context, position *Position) (nodeapi.Position, error) {
	var current Session

	read := func(dbtx *gorm.DB) error {
		if err := dbtx.First(position, "address = ?", position.Address).Error; err != nil {
			return fmt.Errorf("loading position view: %w", err)
		}

		if position.Kind == KindLease {
			return nil
		}

		err := dbtx.Where("subject = ? AND status = ?", position.Address, SessionStatusActive).First(&current).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("loading session consumption: %w", err)
		}

		return nil
	}
	if err := r.db.WithContext(ctx).Transaction(read); err != nil {
		return nodeapi.Position{}, fmt.Errorf("reading position view: %w", err)
	}

	terms := r.termsFor(position, &current)

	var sess *Session
	if current.ID != "" {
		sess = &current
	}

	r.mu.Lock()

	var meter *session.Meter
	if sess != nil {
		meter, _ = r.enforcers[sess.ID].(*session.Meter)
	}

	consumption, err := buildConsumption(position, sess, meter, time.Now().Unix())
	r.mu.Unlock()

	if err != nil {
		return nodeapi.Position{}, err
	}

	return nodeapi.Position{
		Consumption: consumption,
		Ledger:      ledgerOf(position, terms),
	}, nil
}

func (r *Runtime) reloadSessionView(ctx context.Context, sess *Session) (nodeapi.Position, error) {
	var (
		current Session
		view    nodeapi.Position
	)

	read := func(dbtx *gorm.DB) error {
		if err := dbtx.Where("id = ? AND subject = ?", sess.ID, sess.Subject).First(&current).Error; err != nil {
			return fmt.Errorf("loading session consumption: %w", err)
		}

		var position *Position
		if current.Credential != CredentialTicket {
			position = &Position{}
			if err := dbtx.First(position, "address = ?", current.Subject).Error; err != nil {
				return fmt.Errorf("loading session position: %w", err)
			}
		}

		r.mu.Lock()
		meter, _ := r.enforcers[current.ID].(*session.Meter)
		consumption, err := buildConsumption(position, &current, meter, time.Now().Unix())
		r.mu.Unlock()

		if err != nil {
			return err
		}

		view.Consumption = consumption
		if position != nil {
			view.Ledger = ledgerOf(position, session.Terms{
				Basis: session.Basis(current.Basis),
				Price: current.Price,
			})
		}

		return nil
	}
	if err := r.db.WithContext(ctx).Transaction(read); err != nil {
		return nodeapi.Position{}, fmt.Errorf("reading session view: %w", err)
	}

	*sess = current

	return view, nil
}

func (r *Runtime) termsFor(position *Position, sess *Session) session.Terms {
	if position.Kind == KindLease {
		return session.Terms{
			Basis: session.BasisPerHour,
			Price: position.RatePerHour,
		}
	}

	if sess.ID != "" {
		return session.Terms{
			Basis: session.Basis(sess.Basis),
			Price: sess.Price,
		}
	}

	if position.Mode == dvpn.PositionModeStream {
		return session.Terms{
			Basis: session.BasisPerHour,
			Price: position.RatePerHour,
		}
	}

	_, chainNode := r.chainState()

	return session.Terms{
		Basis: session.BasisPerGB,
		Price: chainNode.ChannelPricePerGB,
	}
}

func buildConsumption(
	position *Position,
	sess *Session,
	meter *session.Meter,
	now int64,
) (nodeapi.Consumption, error) {
	var usage session.Usage
	if sess != nil {
		usage = sess.Usage()
		if sess.Status == SessionStatusActive {
			usage.Seconds = uint64(max(now-sess.CreatedUnix, 0))
		}
	}

	if position == nil {
		return nodeapi.Consumption{Usage: usage}, nil
	}

	consumption := nodeapi.Consumption{
		OwedBaseUnits:    position.Settled,
		SettledBaseUnits: position.Settled,
	}
	if sess != nil && sess.Status != SessionStatusActive {
		return consumption, nil
	}

	if position.Kind == KindLease {
		consumption.Usage.Seconds = uint64(max(now-position.OpenedUnix, 0))
		consumption.OwedBaseUnits = session.Owed(
			session.BasisPerHour, position.RatePerHour, consumption.Usage.Seconds,
		)
	} else if sess != nil && sess.Status == SessionStatusActive {
		consumption.Usage = usage

		if position.Mode == dvpn.PositionModeVoucher {
			if meter == nil {
				return nodeapi.Consumption{}, errMissingMeter
			}

			consumption.OwedBaseUnits = meter.Owed(consumption.Usage)
		}
	}

	if position.Mode == dvpn.PositionModeStream {
		consumption.OwedBaseUnits = program.StreamAccrued(
			position.Escrow, position.StreamBase, position.RatePerHour, position.StreamStartedUnix, now,
		)
	}

	return consumption, nil
}

func ledgerOf(position *Position, terms session.Terms) nodeapi.Ledger {
	return nodeapi.Ledger{
		Basis:               terms.Basis,
		CumulativeBaseUnits: position.Cumulative,
		EscrowBaseUnits:     position.Escrow,
		OpenedAt:            position.OpenedUnix,
		Price:               terms.Price,
		RedeemedBaseUnits:   position.Redeemed,
		Signature:           position.Signature,
		Status:              nodeapi.LedgerStatus(position.Status),
	}
}
