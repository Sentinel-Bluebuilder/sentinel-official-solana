package hysteria2

import (
	"embed"
	"fmt"
	"regexp"

	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
)

const Name service.Name = "hysteria2"

const (
	binary             = "hysteria2"
	configFlag         = "-c"
	protocolBit uint32 = 1 << 5
	secretBytes        = 16
	toolDir            = "/etc/hysteria/" + instance.ProductName
)

var (
	obfsPasswordPattern = regexp.MustCompile(`^[\x21\x23-\x5b\x5d-\x7e]{0,64}$`)
	tlsPinPattern       = regexp.MustCompile(`^([0-9A-F]{2}:){31}[0-9A-F]{2}$`)
)

//go:embed templates/*.tmpl
var templates embed.FS

type ClientConfig struct{}

type Metadata struct {
	ObfsPassword string `json:"obfs_password"`
	Port         uint16 `json:"port"`
	TLSPin       string `json:"tls_pin"`
}

func (m Metadata) Validate() error {
	return validateEndpoint(validate.ErrInvalidConfig, m.ObfsPassword, m.Port, m.TLSPin)
}

type PeerRequest struct {
	UUID string `json:"uuid"`
}

func (r PeerRequest) Validate() error {
	return service.ValidateUUID(r.UUID)
}

type PeerResponse struct {
	Auth         string `json:"auth"`
	ObfsPassword string `json:"obfs_password"`
	Port         uint16 `json:"port"`
	TLSPin       string `json:"tls_pin"`
}

func (r PeerResponse) Validate() error {
	if err := service.ValidateUUID(r.Auth); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	return validateEndpoint(service.ErrInvalidPeer, r.ObfsPassword, r.Port, r.TLSPin)
}

type ServerConfig struct {
	AuthPort      uint16 `toml:"auth_port"      validate:"gte=1024,nefield=StatsPort"`
	ListenPort    uint16 `toml:"listen_port"    validate:"gte=1024,nefield=AuthPort,nefield=StatsPort"`
	MasqueradeURL string `toml:"masquerade_url"`
	ObfsPassword  string `toml:"obfs_password"`
	StatsPort     uint16 `toml:"stats_port"     validate:"gte=1024"`
	StatsSecret   string `toml:"stats_secret"   validate:"required"`
}

func (c ServerConfig) Validate() error {
	if err := validate.Struct(c); err != nil {
		return err
	}

	if !obfsPasswordPattern.MatchString(c.ObfsPassword) {
		return fmt.Errorf("%w: obfs_password", validate.ErrInvalidConfig)
	}

	return nil
}

//nolint:gochecknoinits
func init() {
	driver := service.ClientDriver[ClientConfig]{
		Binaries: []string{binary},
		Bit:      protocolBit,
		Defaults: DefaultClientConfig,
		Metadata: func() service.Validator { return &Metadata{} },
		New:      NewClient,
		ToolDir:  toolDir,
	}
	service.RegisterClient(Name, driver)
}

func DefaultClientConfig() ClientConfig {
	return ClientConfig{}
}

func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		AuthPort:     service.RandomPort(),
		ListenPort:   service.RandomPort(),
		ObfsPassword: service.RandomHex(secretBytes),
		StatsPort:    service.RandomPort(),
		StatsSecret:  service.RandomHex(secretBytes),
	}
}

func clientInterface(iface, goos string) string {
	if goos == "darwin" {
		return ""
	}

	return iface
}

func validateEndpoint(base error, obfsPassword string, port uint16, tlsPin string) error {
	if !obfsPasswordPattern.MatchString(obfsPassword) {
		return fmt.Errorf("%w: obfs_password", base)
	}

	if port == 0 {
		return fmt.Errorf("%w: port 0", base)
	}

	if !tlsPinPattern.MatchString(tlsPin) {
		return fmt.Errorf("%w: tls_pin", base)
	}

	return nil
}
