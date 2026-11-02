package config

const (
	LeaseModeStream  = "stream"
	LeaseModeVoucher = "voucher"
)

type Provider struct {
	API     API           `toml:"api"`
	ID      string        `toml:"id"      validate:"required,len=64,hexadecimal"`
	Key     Key           `toml:"key"`
	Lease   ProviderLease `toml:"lease"`
	Owner   string        `toml:"owner"   validate:"required"`
	Tickets Tickets       `toml:"tickets"`
}

func (p *Provider) SetDefaults() {
	if p.Key.Name == "" {
		p.Key.Name = operatorKeyName
	}
}

type ProviderLease struct {
	MaxPrepaySeconds   uint64 `default:"14400"       toml:"max_prepay_seconds"   validate:"gt=0"`
	Mode               string `default:"voucher"     toml:"mode"                 validate:"oneof=voucher stream"`
	NodeTimeoutSeconds uint64 `default:"30"          toml:"node_timeout_seconds" validate:"gt=0"`
	PrepaySeconds      uint64 `toml:"prepay_seconds"`
	ReserveHours       uint64 `default:"24"          toml:"reserve_hours"        validate:"gt=0"`
	TolerancePercent   uint8  `default:"5"           toml:"tolerance_percent"    validate:"gt=0,lte=100"`
	TopupHours         uint64 `default:"24"          toml:"topup_hours"          validate:"gt=0"`
}

type Tickets struct {
	DataCapBytes  uint64 `toml:"data_cap_bytes"`
	MaxTTLSeconds uint64 `default:"86400"       toml:"max_ttl_seconds" validate:"gt=0"`
}

func DefaultProvider() Provider {
	return Provider{
		API: API{Port: 8443},
		Key: Key{Name: operatorKeyName},
		Lease: ProviderLease{
			MaxPrepaySeconds:   14400,
			Mode:               LeaseModeVoucher,
			NodeTimeoutSeconds: 30,
			ReserveHours:       24,
			TolerancePercent:   5,
			TopupHours:         24,
		},
		Tickets: Tickets{MaxTTLSeconds: 86400},
	}
}
