//go:build linux

package provider

const (
	StatusClosed  = "closed"
	StatusClosing = "closing"
	StatusOpen    = "open"
)

type Lease struct {
	Address           string `gorm:"primaryKey"`
	Cumulative        uint64
	Escrow            uint64
	Healthy           bool
	Mode              uint8
	Node              string `gorm:"index"`
	NodeOpenedUnix    int64
	OpenedUnix        int64
	RatePerHour       uint64
	Redeemed          uint64
	Signature         []byte
	Status            string
	StreamBase        uint64
	StreamStartedUnix int64
	UpdatedUnix       int64 `gorm:"autoUpdateTime"`
}

type Plan struct {
	Address         string `gorm:"primaryKey"`
	DataCapGB       uint32
	DurationSeconds uint32
	MaxConnections  uint8
	Price           uint64
	Status          uint8
	UpdatedUnix     int64 `gorm:"autoUpdateTime"`
}

type Probe struct {
	APIURL                string
	DeadSinceUnix         int64
	HeartbeatSeconds      uint64
	LastLiveUnix          int64
	Live                  bool
	MinLeasePrepaySeconds uint64
	Node                  string `gorm:"primaryKey"`
	Owner                 string
	ProbedUnix            int64
	TLSSHA256             string
}

type RosterNode struct {
	AddedUnix int64
	Node      string `gorm:"primaryKey"`
	Plan      string `gorm:"primaryKey"`
}

type Subscription struct {
	ActiveTickets  uint32
	Address        string `gorm:"primaryKey"`
	AllocatedBytes uint64
	OpenedUnix     int64 `gorm:"primaryKey"`
	UpdatedUnix    int64 `gorm:"autoUpdateTime"`
}

type Ticket struct {
	DataCapBytes uint64
	ExpiresUnix  int64
	Hash         string `gorm:"primaryKey"`
	IssuedUnix   int64
	Node         string
	Subscription string `gorm:"index"`
	User         string
}
