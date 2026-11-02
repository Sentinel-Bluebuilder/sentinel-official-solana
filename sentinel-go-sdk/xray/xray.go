package xray

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"fmt"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
	"github.com/sentinel-official/sentinel-go-sdk/v3/internal/v2raycore"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
	"github.com/sentinel-official/sentinel-go-sdk/v3/xray/xraypb"
)

const Name service.Name = "xray"

const (
	ProtocolTrojan  = "trojan"
	ProtocolVLESS   = "vless"
	ProtocolVMess   = "vmess"
	SecurityNone    = "none"
	SecurityReality = "reality"
	SecurityTLS     = "tls"
	TransportGRPC   = "grpc"
	TransportTCP    = "tcp"
	TransportWS     = "ws"
)

const (
	binary                    = "xray"
	defaultDest               = "www.apple.com:443"
	defaultFingerprint        = "chrome"
	defaultProxyPort          = 1080
	defaultServerName         = "www.apple.com"
	protocolBit        uint32 = 1 << 4
	publicKeyBytes            = 32
	shortIDBytes              = 8
	toolDir                   = "/usr/local/etc/xray/" + instance.ProductName
)

//nolint:gochecknoglobals
var (
	fingerprints = []string{
		"chrome", "firefox", "safari", "ios", "android", "edge", "360", "qq", "random", "randomized",
	}
	shortIDPattern = regexp.MustCompile(`^([0-9a-f]{2}){0,8}$`)
)

//go:embed templates/*.tmpl
var templates embed.FS

type ClientConfig struct {
	APIPort   uint16 `toml:"api_port"   validate:"gte=1024"`
	ProxyPort uint16 `toml:"proxy_port" validate:"gte=1"`
}

func (c ClientConfig) Validate() error {
	return validate.Struct(c)
}

func (c ClientConfig) toCore() v2raycore.ClientConfig[Extensions] {
	return v2raycore.ClientConfig[Extensions]{
		APIPort:   c.APIPort,
		ProxyPort: c.ProxyPort,
	}
}

type Extensions struct {
	Reality *Reality `json:"reality,omitzero"`
}

func (e Extensions) Validate(stream v2raycore.Stream) error {
	if (e.Reality != nil) != (stream.Security == SecurityReality) {
		return fmt.Errorf("%w: reality with security %q", service.ErrInvalidPeer, stream.Security)
	}

	if e.Reality == nil {
		return nil
	}

	if !slices.Contains(fingerprints, e.Reality.Fingerprint) {
		return fmt.Errorf("%w: reality fingerprint %q", service.ErrInvalidPeer, e.Reality.Fingerprint)
	}

	if !validPublicKey(e.Reality.PublicKey) {
		return fmt.Errorf("%w: reality public_key %q", service.ErrInvalidPeer, e.Reality.PublicKey)
	}

	if err := service.ValidateHostname(e.Reality.ServerName); err != nil {
		return fmt.Errorf("%w: reality server_name: %w", service.ErrInvalidPeer, err)
	}

	if !shortIDPattern.MatchString(e.Reality.ShortID) {
		return fmt.Errorf("%w: reality short_id %q", service.ErrInvalidPeer, e.Reality.ShortID)
	}

	return nil
}

type Inbound struct {
	Port      uint16   `toml:"port"      validate:"gte=1024"`
	Protocol  string   `toml:"protocol"  validate:"oneof=vless vmess trojan"`
	Reality   *Reality `toml:"reality"   validate:"required_if=Security reality"`
	Security  string   `toml:"security"  validate:"oneof=none tls reality"`
	Transport string   `toml:"transport" validate:"oneof=tcp ws grpc"`
}

func (i Inbound) Validate() error {
	if err := validate.Struct(i); err != nil {
		return err
	}

	if i.Security == SecurityReality && i.Protocol != ProtocolVLESS {
		return fmt.Errorf("%w: reality needs vless", validate.ErrInvalidConfig)
	}

	if i.Security == SecurityReality && i.Transport != TransportTCP && i.Transport != TransportGRPC {
		return fmt.Errorf("%w: reality needs the tcp or grpc transport", validate.ErrInvalidConfig)
	}

	if i.Security == SecurityReality {
		if err := validateRealityDest(i.Reality.Dest); err != nil {
			return err
		}

		stream := v2raycore.Stream{Security: i.Security}
		if err := (Extensions{Reality: i.Reality}).Validate(stream); err != nil {
			return fmt.Errorf("%w: %s", validate.ErrInvalidConfig, err.Error())
		}
	}

	return nil
}

func (i Inbound) toCore() v2raycore.Inbound[Extensions] {
	in := v2raycore.Inbound[Extensions]{
		Port:      i.Port,
		Protocol:  i.Protocol,
		Security:  i.Security,
		Transport: i.Transport,
	}

	if i.Security == SecurityReality {
		in.Extensions = Extensions{Reality: i.Reality}
	}

	return in
}

type Reality struct {
	Dest        string `json:"-"           toml:"dest"`
	Fingerprint string `json:"fingerprint" toml:"fingerprint"`
	PrivateKey  string `json:"-"           toml:"private_key"`
	PublicKey   string `json:"public_key"  toml:"public_key"`
	ServerName  string `json:"server_name" toml:"server_name"`
	ShortID     string `json:"short_id"    toml:"short_id"`
}

func NewReality() (*Reality, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating reality key: %w", err)
	}

	return &Reality{
		Dest:        defaultDest,
		Fingerprint: defaultFingerprint,
		PrivateKey:  base64.RawURLEncoding.EncodeToString(priv.Bytes()),
		PublicKey:   base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
		ServerName:  defaultServerName,
		ShortID:     service.RandomHex(shortIDBytes),
	}, nil
}

type ServerConfig struct {
	APIPort  uint16    `toml:"api_port" validate:"gte=1024"`
	Inbounds []Inbound `toml:"inbounds" validate:"min=1,max=16,dive"`
}

func (c ServerConfig) Validate() error {
	if err := validate.Struct(c); err != nil {
		return err
	}

	for _, in := range c.Inbounds {
		if err := in.Validate(); err != nil {
			return err
		}
	}

	return nil
}

func (c ServerConfig) toCore() v2raycore.ServerConfig[Extensions] {
	inbounds := make([]v2raycore.Inbound[Extensions], 0, len(c.Inbounds))
	for _, in := range c.Inbounds {
		inbounds = append(inbounds, in.toCore())
	}

	return v2raycore.ServerConfig[Extensions]{
		APIPort:  c.APIPort,
		Inbounds: inbounds,
	}
}

var _ v2raycore.API = api{}

type api struct{}

func (a api) AddUser(ctx context.Context, conn grpc.ClientConnInterface, tag, protocol, id string) error {
	account, typeName, err := accountFor(protocol, id)
	if err != nil {
		return err
	}

	raw, err := proto.Marshal(account)
	if err != nil {
		return fmt.Errorf("packing account: %w", err)
	}

	typedAccount := &xraypb.TypedMessage{
		Type:  "xray." + typeName,
		Value: raw,
	}
	user := &xraypb.User{
		Account: typedAccount,
		Email:   id,
	}
	op := &xraypb.AddUserOperation{User: user}

	opRaw, err := proto.Marshal(op)
	if err != nil {
		return fmt.Errorf("packing add user operation: %w", err)
	}

	operation := &xraypb.TypedMessage{
		Type:  "xray.app.proxyman.command.AddUserOperation",
		Value: opRaw,
	}
	req := &xraypb.AlterInboundRequest{
		Operation: operation,
		Tag:       tag,
	}

	if _, err := xraypb.NewHandlerServiceClient(conn).AlterInbound(ctx, req); err != nil {
		return fmt.Errorf("altering inbound %s: %w", tag, err)
	}

	return nil
}

func (a api) QueryStats(
	ctx context.Context,
	conn grpc.ClientConnInterface,
	pattern string,
) (map[string]int64, error) {
	req := &xraypb.QueryStatsRequest{Pattern: pattern}

	resp, err := xraypb.NewStatsServiceClient(conn).QueryStats(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("querying stats: %w", err)
	}

	out := make(map[string]int64, len(resp.GetStat()))
	for _, s := range resp.GetStat() {
		out[s.GetName()] = s.GetValue()
	}

	return out, nil
}

func (a api) RemoveUser(ctx context.Context, conn grpc.ClientConnInterface, tag, id string) error {
	op := &xraypb.RemoveUserOperation{Email: id}

	opRaw, err := proto.Marshal(op)
	if err != nil {
		return fmt.Errorf("packing remove user operation: %w", err)
	}

	operation := &xraypb.TypedMessage{
		Type:  "xray.app.proxyman.command.RemoveUserOperation",
		Value: opRaw,
	}
	req := &xraypb.AlterInboundRequest{
		Operation: operation,
		Tag:       tag,
	}

	if _, err := xraypb.NewHandlerServiceClient(conn).AlterInbound(ctx, req); err != nil {
		return fmt.Errorf("altering inbound %s: %w", tag, err)
	}

	return nil
}

//nolint:gochecknoinits
func init() {
	driver := service.ClientDriver[ClientConfig]{
		Binaries: []string{binary},
		Bit:      protocolBit,
		Defaults: DefaultClientConfig,
		Metadata: func() service.Validator { return &v2raycore.Metadata[Extensions]{} },
		New:      NewClient,
		ToolDir:  toolDir,
	}
	service.RegisterClient(Name, driver)
}

func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		APIPort:   service.RandomPort(),
		ProxyPort: defaultProxyPort,
	}
}

func DefaultServerConfig() ServerConfig {
	cfg := ServerConfig{APIPort: service.RandomPort()}
	cfg.Inbounds = append(cfg.Inbounds, Inbound{
		Port:      service.RandomPort(),
		Protocol:  ProtocolVLESS,
		Security:  SecurityTLS,
		Transport: TransportWS,
	})
	cfg.Inbounds = append(cfg.Inbounds, Inbound{
		Port:      service.RandomPort(),
		Protocol:  ProtocolVMess,
		Security:  SecurityTLS,
		Transport: TransportGRPC,
	})

	reality, err := NewReality()
	if err != nil {
		panic(err)
	}

	cfg.Inbounds = append(cfg.Inbounds, Inbound{
		Port:      service.RandomPort(),
		Protocol:  ProtocolVLESS,
		Reality:   reality,
		Security:  SecurityReality,
		Transport: TransportTCP,
	})

	return cfg
}

func NewClient(cfg ClientConfig, p service.Placement, log zerolog.Logger) (service.Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return v2raycore.NewClient(variant(), cfg.toCore(), p, log)
}

func accountFor(protocol, id string) (account proto.Message, typeName string, err error) {
	switch protocol {
	case ProtocolVLESS:
		account := &xraypb.VlessAccount{
			Encryption: "none",
			Id:         id,
		}

		return account, "proxy.vless.Account", nil
	case ProtocolVMess:
		return &xraypb.VmessAccount{Id: id}, "proxy.vmess.Account", nil
	case ProtocolTrojan:
		return &xraypb.TrojanAccount{Password: id}, "proxy.trojan.Account", nil
	default:
		return nil, "", fmt.Errorf("unsupported protocol %q", protocol)
	}
}

func validateRealityDest(dest string) error {
	host, port, err := net.SplitHostPort(dest)
	if err != nil || host == "" {
		return fmt.Errorf("%w: reality dest %q", validate.ErrInvalidConfig, dest)
	}

	for _, r := range dest {
		if r == '"' || r == '\\' || unicode.IsControl(r) {
			return fmt.Errorf("%w: reality dest %q", validate.ErrInvalidConfig, dest)
		}
	}

	portNum, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNum == 0 {
		return fmt.Errorf("%w: reality dest %q", validate.ErrInvalidConfig, dest)
	}

	return nil
}

func validPublicKey(key string) bool {
	if strings.ContainsAny(key, "\r\n") {
		return false
	}

	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, enc := range encodings {
		if raw, err := enc.Strict().DecodeString(key); err == nil && len(raw) == publicKeyBytes {
			return true
		}
	}

	return false
}

func variant() v2raycore.Variant {
	return v2raycore.Variant{
		API:                 api{},
		Binary:              binary,
		Name:                Name,
		SecurityPreferences: []string{SecurityReality, SecurityTLS},
		Templates:           templates,
	}
}
