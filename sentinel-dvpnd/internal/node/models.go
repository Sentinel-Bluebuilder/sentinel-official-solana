//go:build linux

package node

import (
	"encoding/json"
	"sort"

	"github.com/solana-foundation/solana-go/v2"

	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
)

const (
	CredentialChannel = "channel"
	CredentialTicket  = "ticket"
	KindChannel       = "channel"
	KindLease         = "lease"

	PositionStatusClosed  = "closed"
	PositionStatusClosing = "closing"
	PositionStatusOpen    = "open"

	RedemptionStatusConfirmed = "confirmed"
	RedemptionStatusFailed    = "failed"
	RedemptionStatusPending   = "pending"

	SessionStatusActive  = "active"
	SessionStatusClosed  = "closed"
	SessionStatusClosing = "closing"
)

type Position struct {
	AcceptedUnix      int64
	Address           string `gorm:"primaryKey"`
	ChainSlot         uint64 `gorm:"not null;default:0"`
	CloseStartedUnix  int64
	Cumulative        uint64
	Escrow            uint64
	Kind              string `gorm:"index"`
	Mode              uint8
	NodeOpenedUnix    int64
	OpenedUnix        int64
	Opener            string
	Owner             string
	Provider          string `gorm:"index"`
	RatePerHour       uint64
	Redeemed          uint64
	RedeemedUnix      int64
	Settled           uint64 `gorm:"not null;default:0"`
	Signature         []byte
	Status            string `gorm:"index"`
	StreamBase        uint64
	StreamStartedUnix int64
	UpdatedUnix       int64 `gorm:"autoUpdateTime"`
	VoucherSigner     string
}

type Redemption struct {
	ConfirmedUnix int64
	Error         string
	Kind          string
	SentUnix      int64
	Signature     string `gorm:"primaryKey"`
	Status        string `gorm:"index"`
	Subjects      string
}

type Session struct {
	Basis           uint8
	CloseReason     string
	CreatedUnix     int64  `gorm:"autoCreateTime"`
	Credential      string `gorm:"index"`
	ID              string `gorm:"primaryKey"`
	LastTrafficUnix int64
	PeerID          string `gorm:"uniqueIndex:idx_live_peer,where:status != 'closed'"`
	PeerRequest     []byte
	PeerResponse    []byte
	Price           uint64
	RxBytes         uint64
	RxCounter       uint64
	Seconds         uint64
	SettledUnix     int64
	Signer          string
	Status          string `gorm:"index"`
	Subject         string `gorm:"uniqueIndex:idx_active_subject,where:status = 'active'"`
	TxBytes         uint64
	TxCounter       uint64
	UpdatedUnix     int64 `gorm:"autoUpdateTime"`
}

func (sess *Session) Usage() session.Usage {
	return session.Usage{
		RxBytes: sess.RxBytes,
		Seconds: sess.Seconds,
		TxBytes: sess.TxBytes,
	}
}

type Ticket struct {
	ConsumedBytes uint64
	DataCapBytes  uint64
	ExpiresUnix   int64
	Hash          string `gorm:"primaryKey"`
	Lease         string
	LeaseHealthy  bool
	Provider      string
	Session       string
	Subscription  string
	User          string
}

func formatSubjects(subjects []solana.PublicKey) string {
	names := make([]string, 0, len(subjects))
	for _, subject := range subjects {
		names = append(names, subject.String())
	}

	sort.Strings(names)

	raw, err := json.Marshal(names)
	if err != nil {
		return "[]"
	}

	return string(raw)
}

func parseSubjects(raw string) []string {
	var subjects []string
	if err := json.Unmarshal([]byte(raw), &subjects); err != nil {
		return nil
	}

	return subjects
}
