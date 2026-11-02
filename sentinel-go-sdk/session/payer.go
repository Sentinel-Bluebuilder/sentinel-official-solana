package session

import (
	"errors"
)

var (
	ErrEscrowExhausted      = errors.New("escrow exhausted")
	ErrInvalidLedger        = errors.New("node ledger is not signed by this operator key")
	ErrLedgerMismatch       = errors.New("node ledger disagrees with the chain")
	ErrNodePriceChanged     = errors.New("node price differs from the listing")
	ErrOwedExceedsPaid      = errors.New("node claims unpaid usage")
	ErrOwedMismatch         = errors.New("node owed does not match its usage")
	ErrPrepayExceedsMaximum = errors.New("node minimum prepay exceeds the configured maximum")
	ErrPrepayTooSmall       = errors.New("prepay block costs fewer than two base units")
	ErrPrepayZero           = errors.New("prepay block is zero")
	ErrSettledChanged       = errors.New("node settled changed during the session")
	ErrSettledExceedsBound  = errors.New("node settled exceeds the recorded sessions")
	ErrUsageMismatch        = errors.New("node usage exceeds local usage beyond tolerance")
)

type Payer struct {
	block            uint64
	escrow           uint64
	paid             uint64
	settled          uint64
	terms            Terms
	tolerancePercent uint8
}

func NewPayer(terms Terms, block, escrow uint64, tolerancePercent uint8) *Payer {
	return &Payer{
		block:            block,
		escrow:           escrow,
		terms:            terms,
		tolerancePercent: tolerancePercent,
	}
}

func (p *Payer) Audit(local, node Usage, nodeSettled, nodeOwed uint64) error {
	localAmount := local.Amount(p.terms.Basis)
	nodeAmount := node.Amount(p.terms.Basis)

	tolerance := Tolerance(localAmount, p.block, p.tolerancePercent)
	if nodeAmount > saturatingAdd(localAmount, tolerance) {
		return ErrUsageMismatch
	}

	if nodeSettled != p.settled {
		return ErrSettledChanged
	}

	if nodeOwed != saturatingAdd(p.settled, Owed(p.terms.Basis, p.terms.Price, nodeAmount)) {
		return ErrOwedMismatch
	}

	return nil
}

func (p *Payer) Next(local, node Usage) (cumulative uint64, issue bool) {
	localOwed := Owed(p.terms.Basis, p.terms.Price, local.Amount(p.terms.Basis))
	nodeOwed := Owed(p.terms.Basis, p.terms.Price, node.Amount(p.terms.Basis))
	owed := saturatingAdd(p.settled, max(localOwed, nodeOwed))

	blockOwed := Owed(p.terms.Basis, p.terms.Price, p.block)
	if Headroom(p.paid, owed) > blockOwed/2 || p.paid >= p.escrow {
		return p.paid, false
	}

	cumulative = min(p.escrow, saturatingAdd(owed, blockOwed))

	return cumulative, cumulative > p.paid
}

func (p *Payer) Opening(owed, minBlock uint64) (uint64, error) {
	if p.block == 0 {
		return 0, ErrPrepayZero
	}

	blockOwed := Owed(p.terms.Basis, p.terms.Price, p.block)
	if p.terms.Price > 0 && blockOwed < 2 {
		return 0, ErrPrepayTooSmall
	}

	cumulative := min(p.escrow, max(p.paid, saturatingAdd(owed, blockOwed)))

	minimumOwed := Owed(p.terms.Basis, p.terms.Price, minBlock)
	if Headroom(cumulative, owed) < minimumOwed/2 {
		return cumulative, ErrEscrowExhausted
	}

	return cumulative, nil
}

func (p *Payer) Paid() uint64 {
	return p.paid
}

func (p *Payer) RecordIssued(cumulative uint64) {
	p.paid = max(p.paid, cumulative)
}

func (p *Payer) Resume(paid, settled uint64) {
	p.paid = max(p.paid, paid)
	p.settled = settled
}

func PrepayBlock(terms Terms, configured, minimum, maximum uint64) (uint64, error) {
	if minimum > maximum {
		return 0, ErrPrepayExceedsMaximum
	}

	var floor uint64
	if terms.Price > 0 {
		floor = terms.Basis.Unit()/terms.Price + 1
	}

	block := min(max(configured, minimum, floor), maximum)
	if block == 0 {
		return 0, ErrPrepayZero
	}

	if terms.Price > 0 && Owed(terms.Basis, terms.Price, block) < 2 {
		return 0, ErrPrepayTooSmall
	}

	return block, nil
}
