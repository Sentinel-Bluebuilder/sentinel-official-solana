package session

import (
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
)

type Enforcer interface {
	Exhausted(u Usage, now int64) bool
}

type Meter struct {
	paid    uint64
	settled uint64
	terms   Terms
}

func NewMeter(terms Terms, paid, settled uint64) *Meter {
	return &Meter{
		paid:    paid,
		settled: settled,
		terms:   terms,
	}
}

func (m *Meter) Exhausted(usage Usage, _ int64) bool {
	if m.terms.Price == 0 {
		return m.settled > m.paid
	}

	return m.Owed(usage) >= m.paid
}

func (m *Meter) Headroom(usage Usage) uint64 {
	return Headroom(m.paid, m.Owed(usage))
}

func (m *Meter) Owed(usage Usage) uint64 {
	return saturatingAdd(m.settled, Owed(m.terms.Basis, m.terms.Price, usage.Amount(m.terms.Basis)))
}

func (m *Meter) Paid() uint64 {
	return m.paid
}

func (m *Meter) Settled() uint64 {
	return m.settled
}

func (m *Meter) Update(paid uint64) {
	m.paid = max(m.paid, paid)
}

type StreamEnforcer struct {
	escrow      uint64
	ratePerHour uint64
	startedUnix int64
	streamBase  uint64
}

func NewStreamEnforcer(escrow, streamBase, ratePerHour uint64, startedUnix int64) *StreamEnforcer {
	return &StreamEnforcer{
		escrow:      escrow,
		ratePerHour: ratePerHour,
		startedUnix: startedUnix,
		streamBase:  streamBase,
	}
}

func (e *StreamEnforcer) Exhausted(_ Usage, now int64) bool {
	return program.StreamAccrued(e.escrow, e.streamBase, e.ratePerHour, e.startedUnix, now) >= e.escrow
}

func (e *StreamEnforcer) Update(escrow, streamBase uint64, startedUnix int64) {
	e.escrow = escrow
	e.startedUnix = startedUnix
	e.streamBase = streamBase
}

type TicketEnforcer struct {
	dataCapBytes uint64
	expiresUnix  int64
	leaseHealthy bool
}

func NewTicketEnforcer(dataCapBytes uint64, expiresUnix int64) *TicketEnforcer {
	return &TicketEnforcer{
		dataCapBytes: dataCapBytes,
		expiresUnix:  expiresUnix,
		leaseHealthy: true,
	}
}

func (e *TicketEnforcer) CapReached(usage Usage) bool {
	return e.dataCapBytes != 0 && usage.Amount(BasisPerGB) >= e.dataCapBytes
}

func (e *TicketEnforcer) Exhausted(usage Usage, now int64) bool {
	return e.LeaseUnhealthy() || e.Expired(now) || e.CapReached(usage)
}

func (e *TicketEnforcer) Expired(now int64) bool {
	return now >= e.expiresUnix
}

func (e *TicketEnforcer) LeaseUnhealthy() bool {
	return !e.leaseHealthy
}

func (e *TicketEnforcer) SetLeaseHealthy(healthy bool) {
	e.leaseHealthy = healthy
}
