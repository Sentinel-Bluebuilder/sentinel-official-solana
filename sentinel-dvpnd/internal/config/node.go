package config

const operatorKeyName = "operator"

type HandshakeDNS struct {
	Enabled bool `toml:"enabled"`
}

type Node struct {
	API          API          `toml:"api"`
	Country      string       `toml:"country"       validate:"required,len=2,alpha,uppercase"`
	HandshakeDNS HandshakeDNS `toml:"handshake_dns"`
	ID           string       `toml:"id"            validate:"required,len=64,hexadecimal"`
	Key          Key          `toml:"key"`
	Lease        NodeLease    `toml:"lease"`
	Owner        string       `toml:"owner"         validate:"required"`
	PriceFeed    PriceFeed    `toml:"price_feed"`
	Prices       Prices       `toml:"prices"`
	Redeem       Redeem       `toml:"redeem"`
	Service      string       `default:"wireguard"  toml:"service"                            validate:"required"`
	Sessions     Sessions     `toml:"sessions"`
	Speedtest    Speedtest    `toml:"speedtest"`
}

func (n *Node) SetDefaults() {
	if n.Key.Name == "" {
		n.Key.Name = operatorKeyName
	}
}

type NodeLease struct {
	MinPrepaySeconds uint64 `default:"3600" toml:"min_prepay_seconds" validate:"gt=0"`
}

type PriceFeed struct {
	APIKey            string  `toml:"api_key"`
	ChannelPerGBUSD   float64 `toml:"channel_per_gb_usd"`
	ChannelPerHourUSD float64 `toml:"channel_per_hour_usd"`
	Enabled           bool    `toml:"enabled"`
	LeasePerHourUSD   float64 `toml:"lease_per_hour_usd"`
}

type Prices struct {
	ChannelPerGB   uint64 `toml:"channel_per_gb"   validate:"lte=9223372036854775807"`
	ChannelPerHour uint64 `toml:"channel_per_hour" validate:"lte=9223372036854775807"`
	LeasePerHour   uint64 `toml:"lease_per_hour"   validate:"lte=9223372036854775807"`
}

type Redeem struct {
	ThresholdBaseUnits uint64 `default:"10000000" toml:"threshold_base_units"`
}

type Sessions struct {
	HeartbeatSeconds uint64 `default:"5"         toml:"heartbeat_seconds"  validate:"gt=0,lte=60"`
	IdleSeconds      uint64 `default:"900"       toml:"idle_seconds"       validate:"gt=0"`
	MaxPeers         uint   `default:"250"       toml:"max_peers"          validate:"gt=0,lte=250"`
	MinPrepayBytes   uint64 `default:"100000000" toml:"min_prepay_bytes"   validate:"gt=0"`
	MinPrepaySeconds uint64 `default:"60"        toml:"min_prepay_seconds" validate:"gt=0"`
}

type Speedtest struct {
	Enabled bool `default:"true" toml:"enabled"`
}

func DefaultNode() Node {
	return Node{
		API:     API{Port: 8443},
		Key:     Key{Name: operatorKeyName},
		Lease:   NodeLease{MinPrepaySeconds: 3600},
		Redeem:  Redeem{ThresholdBaseUnits: 10_000_000},
		Service: "wireguard",
		Sessions: Sessions{
			HeartbeatSeconds: 5,
			IdleSeconds:      900,
			MaxPeers:         250,
			MinPrepayBytes:   100_000_000,
			MinPrepaySeconds: 60,
		},
		Speedtest: Speedtest{Enabled: true},
	}
}
