package v2ray

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
	"github.com/sentinel-official/sentinel-go-sdk/v3/internal/v2raycore"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/v2ray/v2raypb"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
)

const Name service.Name = "v2ray"

const (
	ProtocolTrojan = "trojan"
	ProtocolVLESS  = "vless"
	ProtocolVMess  = "vmess"
	SecurityNone   = "none"
	SecurityTLS    = "tls"
	TransportGRPC  = "grpc"
	TransportTCP   = "tcp"
	TransportWS    = "ws"
)

const (
	binary                  = "v2ray"
	defaultProxyPort        = 1080
	protocolBit      uint32 = 1 << 1
	securityReality         = "reality"
	toolDir                 = "/usr/local/etc/v2ray/" + instance.ProductName
	typeURLBase             = "types.v2fly.org/v2ray.core."
	versionTimeout          = 5 * time.Second
)

//go:embed templates/*.tmpl
var templates embed.FS

var v4Pattern = regexp.MustCompile(`^V2Ray 4\.\d+\.\d+`)

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

type Extensions struct{}

func (e Extensions) Validate(stream v2raycore.Stream) error {
	if stream.Security == securityReality {
		return fmt.Errorf("%w: security %q", service.ErrInvalidPeer, stream.Security)
	}

	if stream.Flow != "" {
		return fmt.Errorf("%w: flow %q", service.ErrInvalidPeer, stream.Flow)
	}

	return nil
}

type Inbound struct {
	Port      uint16 `toml:"port"      validate:"gte=1024"`
	Protocol  string `toml:"protocol"  validate:"oneof=vless vmess trojan"`
	Security  string `toml:"security"  validate:"oneof=none tls"`
	Transport string `toml:"transport" validate:"oneof=tcp ws grpc"`
}

func (i Inbound) Validate() error {
	return validate.Struct(i)
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

var _ v2raycore.API = api{}

type api struct{}

func (a api) AddUser(ctx context.Context, conn grpc.ClientConnInterface, tag, protocol, id string) error {
	account, typeName, err := accountFor(protocol, id)
	if err != nil {
		return err
	}

	packedAccount, err := anypb.New(account)
	if err != nil {
		return fmt.Errorf("packing account: %w", err)
	}

	packedAccount.TypeUrl = typeURLBase + typeName
	user := &v2raypb.User{
		Account: packedAccount,
		Email:   id,
	}
	op := &v2raypb.AddUserOperation{User: user}

	packedOp, err := anypb.New(op)
	if err != nil {
		return fmt.Errorf("packing add user operation: %w", err)
	}

	packedOp.TypeUrl = typeURLBase + "app.proxyman.command.AddUserOperation"
	req := &v2raypb.AlterInboundRequest{
		Operation: packedOp,
		Tag:       tag,
	}

	if _, err := v2raypb.NewHandlerServiceClient(conn).AlterInbound(ctx, req); err != nil {
		return fmt.Errorf("altering inbound %s: %w", tag, err)
	}

	return nil
}

func (a api) QueryStats(
	ctx context.Context,
	conn grpc.ClientConnInterface,
	pattern string,
) (map[string]int64, error) {
	req := &v2raypb.QueryStatsRequest{Pattern: pattern}

	resp, err := v2raypb.NewStatsServiceClient(conn).QueryStats(ctx, req)
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
	op := &v2raypb.RemoveUserOperation{Email: id}

	packedOp, err := anypb.New(op)
	if err != nil {
		return fmt.Errorf("packing remove user operation: %w", err)
	}

	packedOp.TypeUrl = typeURLBase + "app.proxyman.command.RemoveUserOperation"
	req := &v2raypb.AlterInboundRequest{
		Operation: packedOp,
		Tag:       tag,
	}

	if _, err := v2raypb.NewHandlerServiceClient(conn).AlterInbound(ctx, req); err != nil {
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
		Preflight: func(ctx context.Context, _ ClientConfig) []service.PreflightCheck {
			return preflightVersion(ctx, service.SeverityWarning)
		},
		ToolDir: toolDir,
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
		account := &v2raypb.VlessAccount{
			Encryption: "none",
			Id:         id,
		}

		return account, "proxy.vless.Account", nil
	case ProtocolVMess:
		return &v2raypb.VmessAccount{Id: id}, "proxy.vmess.Account", nil
	case ProtocolTrojan:
		return &v2raypb.TrojanAccount{Password: id}, "proxy.trojan.Account", nil
	default:
		return nil, "", fmt.Errorf("unsupported protocol %q", protocol)
	}
}

func checkVersion(out []byte, err error, severity service.Severity) []service.PreflightCheck {
	if errors.Is(err, context.DeadlineExceeded) {
		detail := fmt.Sprintf("%s -version did not exit within %s", binary, versionTimeout)

		return []service.PreflightCheck{service.NewPreflightCheck("service_version", service.SeverityWarning, detail)}
	}

	version := v4Pattern.Find(out)
	if err != nil || version == nil {
		return nil
	}

	detail := string(version) + " ignores run -config; install V2Ray 5"

	return []service.PreflightCheck{service.NewPreflightCheck("service_version", severity, detail)}
}

func errorFromChecks(checks []service.PreflightCheck) error {
	for _, check := range checks {
		if !check.OK {
			return errors.New(check.Detail)
		}
	}

	return nil
}

func preflightVersion(ctx context.Context, severity service.Severity) []service.PreflightCheck {
	if _, err := service.LookPath(binary); err != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()

	out, err := service.Run(ctx, service.Command{
		Args: []string{"-version"},
		Path: binary,
	})
	if err != nil && ctx.Err() != nil {
		err = ctx.Err()
	}

	return checkVersion(out, err, severity)
}

func variant() v2raycore.Variant {
	return v2raycore.Variant{
		API:    api{},
		Binary: binary,
		CheckVersion: func(ctx context.Context) error {
			return errorFromChecks(preflightVersion(ctx, service.SeverityError))
		},
		Name:                Name,
		SecurityPreferences: []string{SecurityTLS},
		Templates:           templates,
	}
}
