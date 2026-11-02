package openvpn

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"embed"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strings"

	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
)

const Name service.Name = "openvpn"

const (
	binary                   = "openvpn"
	certificateHeader        = "-----BEGIN CERTIFICATE-----\n"
	clientToolDir            = "/etc/openvpn/client/" + instance.ProductName
	defaultProtocol          = "udp"
	maxCABytes               = 8 << 10
	maxTLSCryptBytes         = 2 << 10
	protocolBit       uint32 = 1 << 2
	tlsCryptFooter           = "-----END OpenVPN Static key V1-----\n"
	tlsCryptHeader           = "-----BEGIN OpenVPN Static key V1-----\n"
	tlsCryptKeyBytes         = 256
	tlsCryptLineChars        = 32
)

//nolint:gochecknoglobals
var (
	lookupDirs      = func() []string { return nil }
	passwordPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	tlsCryptPattern = regexp.MustCompile(`^(#[\x20-\x7e]{0,119}\n){0,8}` + regexp.QuoteMeta(tlsCryptHeader) +
		`([0-9a-f]{32}\n){16}` + regexp.QuoteMeta(tlsCryptFooter) + `$`)
)

//go:embed templates/*.tmpl
var templates embed.FS

type ClientConfig struct {
	ManagementPort uint16 `toml:"management_port" validate:"gte=1024"`
}

func (c ClientConfig) Validate() error {
	return validate.Struct(c)
}

type Metadata struct {
	CA       string `json:"ca"`
	Port     uint16 `json:"port"`
	Protocol string `json:"protocol"`
	TLSCrypt string `json:"tls_crypt"`
}

func (m Metadata) Validate() error {
	if err := validateEndpoint(m.CA, m.Port, m.Protocol, m.TLSCrypt); err != nil {
		return fmt.Errorf("%w: %w", validate.ErrInvalidConfig, err)
	}

	return nil
}

type PeerRequest struct {
	UUID string `json:"uuid"`
}

func (r PeerRequest) Validate() error {
	return service.ValidateUUID(r.UUID)
}

type PeerResponse struct {
	CA       string `json:"ca"`
	Password string `json:"password"`
	Port     uint16 `json:"port"`
	Protocol string `json:"protocol"`
	TLSCrypt string `json:"tls_crypt"`
	Username string `json:"username"`
}

func (r PeerResponse) Validate() error {
	if err := validateEndpoint(r.CA, r.Port, r.Protocol, r.TLSCrypt); err != nil {
		return fmt.Errorf("%w: %w", service.ErrInvalidPeer, err)
	}

	if !passwordPattern.MatchString(r.Password) {
		return fmt.Errorf("%w: password", service.ErrInvalidPeer)
	}

	if service.ValidateUUID(r.Username) != nil {
		return fmt.Errorf("%w: username", service.ErrInvalidPeer)
	}

	return nil
}

type ServerConfig struct {
	Addresses       []string `toml:"addresses"        validate:"min=1,dive,cidr"`
	EgressInterface string   `toml:"egress_interface"`
	ListenPort      uint16   `toml:"listen_port"      validate:"gte=1024,nefield=ManagementPort"`
	ManagementPort  uint16   `toml:"management_port"  validate:"gte=1024"`
	Protocol        string   `toml:"protocol"         validate:"oneof=udp tcp"`
}

func (c ServerConfig) Validate() error {
	if err := validate.Struct(c); err != nil {
		return err
	}

	if c.EgressInterface != "" {
		if err := service.ValidateInterfaceName(c.EgressInterface); err != nil {
			return err
		}
	}

	_, err := service.ParsePrefixes(c.Addresses)

	return err
}

//nolint:gochecknoinits
func init() {
	driver := service.ClientDriver[ClientConfig]{
		Binaries:   []string{binary},
		Bit:        protocolBit,
		Defaults:   DefaultClientConfig,
		LookupDirs: func() []string { return lookupDirs() },
		Metadata:   func() service.Validator { return &Metadata{} },
		New:        NewClient,
		Preflight: func(ctx context.Context, _ ClientConfig) []service.PreflightCheck {
			return preflightDCO(ctx, runtime.GOOS)
		},
		ToolDir: clientToolDir,
	}
	service.RegisterClient(Name, driver)
}

func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		ManagementPort: service.RandomPort(),
	}
}

func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Addresses:      []string{service.RandomPrefix().String()},
		ListenPort:     service.RandomPort(),
		ManagementPort: service.RandomPort(),
		Protocol:       defaultProtocol,
	}
}

func clientInterface(iface, goos string) string {
	if goos == "darwin" {
		return ""
	}

	return iface
}

func newTLSCryptKey() ([]byte, error) {
	raw := make([]byte, tlsCryptKeyBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generating tls-crypt key: %w", err)
	}

	text := hex.EncodeToString(raw)

	var b strings.Builder

	b.WriteString(tlsCryptHeader)

	for i := 0; i < len(text); i += tlsCryptLineChars {
		b.WriteString(text[i : i+tlsCryptLineChars])
		b.WriteByte('\n')
	}

	b.WriteString(tlsCryptFooter)

	return []byte(b.String()), nil
}

func tunDirectives(iface, goos string) (dev, devNode string) {
	switch goos {
	case "darwin":
		return "utun", ""
	case "windows":
		return "tun", iface
	default:
		return iface, ""
	}
}

func validateCA(text string) error {
	if len(text) > maxCABytes {
		return fmt.Errorf("ca: length %d", len(text))
	}

	block, rest := pem.Decode([]byte(text))
	if !strings.HasPrefix(text, certificateHeader) || block == nil || block.Type != "CERTIFICATE" ||
		len(block.Headers) != 0 {
		return errors.New("ca: not a PEM certificate")
	}

	if len(rest) != 0 {
		return fmt.Errorf("ca: %d trailing bytes", len(rest))
	}

	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return fmt.Errorf("ca: %w", err)
	}

	return nil
}

func validateEndpoint(ca string, port uint16, protocol, tlsCrypt string) error {
	if err := validateCA(ca); err != nil {
		return err
	}

	if port == 0 {
		return fmt.Errorf("port %d", port)
	}

	if protocol != "udp" && protocol != "tcp" {
		return fmt.Errorf("protocol %q", protocol)
	}

	return validateTLSCrypt(tlsCrypt)
}

func validateTLSCrypt(text string) error {
	if len(text) > maxTLSCryptBytes {
		return fmt.Errorf("tls_crypt: length %d", len(text))
	}

	if !tlsCryptPattern.MatchString(text) {
		return errors.New("tls_crypt: not an OpenVPN static key V1")
	}

	return nil
}
