package v2raycore

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"slices"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
)

const (
	flowVision     = "xtls-rprx-vision"
	maxInbounds    = 16
	protocolTrojan = "trojan"
	protocolVLESS  = "vless"
	securityTLS    = "tls"
)

//nolint:gochecknoglobals
var (
	confDirEnvs = []string{
		"V2RAY_LOCATION_CONFDIR",
		"XRAY_LOCATION_CONFDIR",
		"v2ray.location.confdir",
		"xray.location.confdir",
	}
	protocols     = []string{protocolTrojan, protocolVLESS, "vmess"}
	securities    = []string{"none", "reality", securityTLS}
	tlsPinPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	transports    = []string{"grpc", "tcp", "ws"}
)

type API interface {
	AddUser(ctx context.Context, conn grpc.ClientConnInterface, tag, protocol, id string) error
	QueryStats(ctx context.Context, conn grpc.ClientConnInterface, pattern string) (map[string]int64, error)
	RemoveUser(ctx context.Context, conn grpc.ClientConnInterface, tag, id string) error
}

type ClientConfig[E Extensions] struct {
	APIPort   uint16
	ProxyPort uint16
}

type Extensions interface {
	Validate(stream Stream) error
}

type Inbound[E Extensions] struct {
	Extensions E      `json:"extensions,omitzero"`
	Flow       string `json:"flow,omitempty"`
	Port       uint16 `json:"port"`
	Protocol   string `json:"protocol"`
	Security   string `json:"security"`
	TLSPin     string `json:"tls_pin,omitempty"`
	Transport  string `json:"transport"`
}

func (i Inbound[E]) Tag() string {
	return fmt.Sprintf("in-%d", i.Port)
}

func (i Inbound[E]) Validate() error {
	if i.Port == 0 {
		return fmt.Errorf("%w: port 0", service.ErrInvalidPeer)
	}

	if !slices.Contains(protocols, i.Protocol) {
		return fmt.Errorf("%w: protocol %q", service.ErrInvalidPeer, i.Protocol)
	}

	if !slices.Contains(transports, i.Transport) {
		return fmt.Errorf("%w: transport %q", service.ErrInvalidPeer, i.Transport)
	}

	if !slices.Contains(securities, i.Security) {
		return fmt.Errorf("%w: security %q", service.ErrInvalidPeer, i.Security)
	}

	if i.Flow != "" && (i.Flow != flowVision || i.Protocol != protocolVLESS) {
		return fmt.Errorf("%w: flow %q with protocol %q", service.ErrInvalidPeer, i.Flow, i.Protocol)
	}

	pinned := i.Security == securityTLS
	if pinned != (i.TLSPin != "") || (pinned && !tlsPinPattern.MatchString(i.TLSPin)) {
		return fmt.Errorf("%w: tls_pin %q with security %q", service.ErrInvalidPeer, i.TLSPin, i.Security)
	}

	if err := i.Extensions.Validate(i.stream()); err != nil {
		return fmt.Errorf("extensions: %w", err)
	}

	return nil
}

func (i Inbound[E]) stream() Stream {
	return Stream{
		Flow:      i.Flow,
		Protocol:  i.Protocol,
		Security:  i.Security,
		TLSPin:    i.TLSPin,
		Transport: i.Transport,
	}
}

type Metadata[E Extensions] struct {
	Inbounds []Inbound[E] `json:"inbounds"`
}

func (m Metadata[E]) Validate() error {
	if err := validateInbounds(m.Inbounds); err != nil {
		return fmt.Errorf("%w: %s", validate.ErrInvalidConfig, err.Error())
	}

	return nil
}

type PeerRequest struct {
	UUID string `json:"uuid"`
}

func (r PeerRequest) Validate() error {
	return service.ValidateUUID(r.UUID)
}

type PeerResponse[E Extensions] struct {
	Inbounds []Inbound[E] `json:"inbounds"`
	UUID     string       `json:"uuid"`
}

func (r PeerResponse[E]) Validate() error {
	if err := service.ValidateUUID(r.UUID); err != nil {
		return err
	}

	return validateInbounds(r.Inbounds)
}

type ServerConfig[E Extensions] struct {
	APIPort  uint16
	Inbounds []Inbound[E]
}

type Stream struct {
	Flow      string
	Protocol  string
	Security  string
	TLSPin    string
	Transport string
}

type Variant struct {
	API                 API
	Binary              string
	CheckVersion        func(ctx context.Context) error
	Name                service.Name
	SecurityPreferences []string
	Templates           fs.FS
}

func dialAPI(ctx context.Context, v Variant, port uint16) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(
		service.LoopbackAddr(port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("dialing %s: %w", v.Binary, err)
	}

	if _, err := v.API.QueryStats(ctx, conn, ""); err != nil {
		_ = conn.Close()

		return nil, fmt.Errorf("dialing %s: %w", v.Binary, err)
	}

	return conn, nil
}

func validateInbounds[E Extensions](inbounds []Inbound[E]) error {
	if len(inbounds) == 0 || len(inbounds) > maxInbounds {
		return fmt.Errorf("%w: %d inbounds", service.ErrInvalidPeer, len(inbounds))
	}

	for n, in := range inbounds {
		if err := in.Validate(); err != nil {
			return fmt.Errorf("inbounds[%d]: %w", n, err)
		}
	}

	return nil
}
