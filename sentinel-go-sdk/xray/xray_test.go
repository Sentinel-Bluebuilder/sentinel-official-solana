package xray_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	"github.com/sentinel-official/sentinel-go-sdk/v3/internal/v2raycore"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
	"github.com/sentinel-official/sentinel-go-sdk/v3/xray"
	"github.com/sentinel-official/sentinel-go-sdk/v3/xray/xraypb"
)

const bufSize = 1 << 20

type fakeHandler struct {
	xraypb.UnimplementedHandlerServiceServer

	altered   []*xraypb.AlterInboundRequest
	rejectTag string
}

func (h *fakeHandler) AlterInbound(
	_ context.Context,
	req *xraypb.AlterInboundRequest,
) (*xraypb.AlterInboundResponse, error) {
	if h.rejectTag != "" && req.GetTag() == h.rejectTag {
		return nil, errors.New("rejected")
	}

	h.altered = append(h.altered, req)

	return &xraypb.AlterInboundResponse{}, nil
}

type fakeStats struct {
	xraypb.UnimplementedStatsServiceServer

	stats map[string]int64
}

func (s *fakeStats) QueryStats(_ context.Context, req *xraypb.QueryStatsRequest) (*xraypb.QueryStatsResponse, error) {
	resp := &xraypb.QueryStatsResponse{}

	for name, value := range s.stats {
		if req.GetPattern() == "" || strings.HasPrefix(name, req.GetPattern()) {
			resp.Stat = append(resp.Stat, &xraypb.Stat{
				Name:  name,
				Value: value,
			})
		}
	}

	return resp, nil
}

func TestAPI_AddUser_VLESS(t *testing.T) {
	t.Parallel()

	handler := &fakeHandler{}
	stats := &fakeStats{}
	conn := dialFake(t, handler, stats)

	require.NoError(t, xray.API{}.AddUser(t.Context(), conn, "in-1", xray.ProtocolVLESS, "uuid-1"))
	require.Len(t, handler.altered, 1)

	req := handler.altered[0]
	require.Equal(t, "in-1", req.GetTag())
	require.Equal(t, "xray.app.proxyman.command.AddUserOperation", req.GetOperation().GetType())

	var op xraypb.AddUserOperation
	require.NoError(t, proto.Unmarshal(req.GetOperation().GetValue(), &op))
	require.Equal(t, "uuid-1", op.GetUser().GetEmail())
	require.Equal(t, "xray.proxy.vless.Account", op.GetUser().GetAccount().GetType())

	var account xraypb.VlessAccount
	require.NoError(t, proto.Unmarshal(op.GetUser().GetAccount().GetValue(), &account))
	require.Equal(t, "uuid-1", account.GetId())
	require.Equal(t, "none", account.GetEncryption())
}

func TestAPI_QueryStats_ByName(t *testing.T) {
	t.Parallel()

	handler := &fakeHandler{}
	stats := &fakeStats{stats: map[string]int64{
		"user>>>uuid-1>>>traffic>>>uplink":   10,
		"user>>>uuid-1>>>traffic>>>downlink": 20,
		"user>>>uuid-2>>>traffic>>>uplink":   30,
	}}
	conn := dialFake(t, handler, stats)

	out, err := xray.API{}.QueryStats(t.Context(), conn, "user>>>uuid-1>>>")
	require.NoError(t, err)
	require.Len(t, out, 2)
	require.EqualValues(t, 10, out["user>>>uuid-1>>>traffic>>>uplink"])
	require.EqualValues(t, 20, out["user>>>uuid-1>>>traffic>>>downlink"])
}

func TestAPI_RemoveUser_Email(t *testing.T) {
	t.Parallel()

	handler := &fakeHandler{}
	stats := &fakeStats{}
	conn := dialFake(t, handler, stats)

	require.NoError(t, xray.API{}.RemoveUser(t.Context(), conn, "in-1", "uuid-1"))
	require.Len(t, handler.altered, 1)

	req := handler.altered[0]
	require.Equal(t, "in-1", req.GetTag())
	require.Equal(t, "xray.app.proxyman.command.RemoveUserOperation", req.GetOperation().GetType())

	var op xraypb.RemoveUserOperation
	require.NoError(t, proto.Unmarshal(req.GetOperation().GetValue(), &op))
	require.Equal(t, "uuid-1", op.GetEmail())
}

func TestDefaultServerConfig(t *testing.T) {
	t.Parallel()

	cfg := xray.DefaultServerConfig()

	require.Len(t, cfg.Inbounds, 3)
	require.True(t, slices.ContainsFunc(cfg.Inbounds, func(in xray.Inbound) bool {
		return in.Security == xray.SecurityReality
	}))
}

func TestExtensions_Validate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		mutate  func(*xray.Reality)
		name    string
		tls     bool
		wantErr bool
	}{
		{
			mutate: func(*xray.Reality) {},
			name:   "reality",
		},
		{
			mutate:  func(*xray.Reality) {},
			name:    "reality with tls",
			tls:     true,
			wantErr: true,
		},
		{
			mutate:  nil,
			name:    "reality missing",
			wantErr: true,
		},
		{
			mutate: func(r *xray.Reality) { r.PublicKey = "+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/v7+/s=" },
			name:   "standard padded key",
		},
		{
			mutate:  func(r *xray.Reality) { r.PublicKey = "-_v7-_v7-_v7-_v7-_v7-_v7-_v7-_v7-_v7-_v7-w" },
			name:    "key of 31 bytes",
			wantErr: true,
		},
		{
			mutate:  func(r *xray.Reality) { r.PublicKey = "-_v7-_v7-_v7-_v7-_v7-_v7-_v7-_v7\n-_v7-_v7-_s" },
			name:    "key with newline",
			wantErr: true,
		},
		{
			mutate:  func(r *xray.Reality) { r.Fingerprint = "ie" },
			name:    "unknown fingerprint",
			wantErr: true,
		},
		{
			mutate:  func(r *xray.Reality) { r.ServerName = "bad_host" },
			name:    "bad server name",
			wantErr: true,
		},
		{
			mutate:  func(r *xray.Reality) { r.ServerName = `a".com` },
			name:    "quoted server name",
			wantErr: true,
		},
		{
			mutate:  func(r *xray.Reality) { r.ShortID = "abc" },
			name:    "odd short id",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ext := xray.Extensions{}
			if tc.mutate != nil {
				ext.Reality = realityFixture()
				tc.mutate(ext.Reality)
			}

			stream := v2raycore.Stream{Security: xray.SecurityReality}
			if tc.tls {
				stream.Security = xray.SecurityTLS
			}

			err := ext.Validate(stream)
			if tc.wantErr {
				require.ErrorIs(t, err, service.ErrInvalidPeer)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestExtensions_Validate_Injection(t *testing.T) {
	t.Parallel()

	fields := map[string]func(*xray.Reality, string){
		"fingerprint": func(r *xray.Reality, v string) { r.Fingerprint += v },
		"public_key":  func(r *xray.Reality, v string) { r.PublicKey += v },
		"server_name": func(r *xray.Reality, v string) { r.ServerName += v },
		"short_id":    func(r *xray.Reality, v string) { r.ShortID += v },
	}

	for field, set := range fields {
		for _, vector := range injectionVectors() {
			t.Run(fmt.Sprintf("%s %q", field, vector), func(t *testing.T) {
				t.Parallel()

				ext := xray.Extensions{Reality: realityFixture()}
				set(ext.Reality, vector)

				err := ext.Validate(v2raycore.Stream{Security: xray.SecurityReality})
				require.ErrorIs(t, err, service.ErrInvalidPeer)
			})
		}
	}
}

func TestInbound_Validate_Reality(t *testing.T) {
	t.Parallel()

	cases := []struct {
		mutate  func(*xray.Reality)
		name    string
		wantErr bool
	}{
		{
			mutate: func(*xray.Reality) {},
			name:   "default",
		},
		{
			mutate:  func(r *xray.Reality) { r.Fingerprint = "netscape" },
			name:    "unknown fingerprint",
			wantErr: true,
		},
		{
			mutate:  func(r *xray.Reality) { r.ShortID = "xyz" },
			name:    "invalid short id",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reality, err := xray.NewReality()
			require.NoError(t, err)

			tc.mutate(reality)
			in := xray.Inbound{
				Port:      30000,
				Protocol:  xray.ProtocolVLESS,
				Reality:   reality,
				Security:  xray.SecurityReality,
				Transport: xray.TransportTCP,
			}

			err = in.Validate()
			if tc.wantErr {
				require.ErrorIs(t, err, validate.ErrInvalidConfig)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestInbound_Validate_RealityDest(t *testing.T) {
	t.Parallel()

	reality, err := xray.NewReality()
	require.NoError(t, err)

	cases := []struct {
		dest    string
		name    string
		wantErr bool
	}{
		{dest: "", name: "empty", wantErr: true},
		{dest: "bogus", name: "bogus", wantErr: true},
		{dest: "example.com", name: "missing port", wantErr: true},
		{dest: "example.com:0", name: "zero port", wantErr: true},
		{dest: "example.com:70000", name: "port too large", wantErr: true},
		{dest: `"a":443`, name: "quoted host", wantErr: true},
		{dest: "example.com\t:443", name: "control character", wantErr: true},
		{dest: "example.com\x7f:443", name: "delete control", wantErr: true},
		{dest: `example\q.com:443`, name: "invalid JSON escape", wantErr: true},
		{dest: "example.com:443", name: "hostname"},
		{dest: "[2001:db8::1]:443", name: "IPv6"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reality := *reality
			reality.Dest = tc.dest
			in := xray.Inbound{
				Port:      30000,
				Protocol:  xray.ProtocolVLESS,
				Reality:   &reality,
				Security:  xray.SecurityReality,
				Transport: xray.TransportTCP,
			}

			err := in.Validate()
			if tc.wantErr {
				require.ErrorIs(t, err, validate.ErrInvalidConfig)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestNewClient_Registered(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"xray"}, service.Binaries(xray.Name))
	sdktest.RequireClientRegistered(t, xray.Name, 1<<4, xray.ClientConfig{})

	dir, err := service.ClientToolDir(xray.Name)
	require.NoError(t, err)
	require.Equal(t, "/usr/local/etc/xray/sentinel-dvpn", dir)
}

func TestReality_JSONSecrets(t *testing.T) {
	t.Parallel()

	r, err := xray.NewReality()
	require.NoError(t, err)

	raw, err := json.Marshal(r)
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	require.Len(t, m, 4)
	require.NotContains(t, m, "dest")
	require.NotContains(t, m, "private_key")
	require.Equal(t, r.PublicKey, m["public_key"])
}

func TestServerConfig_ToCore_Reality(t *testing.T) {
	t.Parallel()

	reality, err := xray.NewReality()
	require.NoError(t, err)

	cfg := xray.ServerConfig{
		APIPort: 10085,
		Inbounds: []xray.Inbound{
			{
				Port:      8443,
				Protocol:  xray.ProtocolVLESS,
				Reality:   reality,
				Security:  xray.SecurityTLS,
				Transport: xray.TransportWS,
			},
			{
				Port:      9443,
				Protocol:  xray.ProtocolVLESS,
				Reality:   reality,
				Security:  xray.SecurityReality,
				Transport: xray.TransportTCP,
			},
		},
	}
	require.NoError(t, cfg.Validate())

	c := xray.ServerConfigToCore(cfg)
	require.Len(t, c.Inbounds, 2)
	require.Nil(t, c.Inbounds[0].Extensions.Reality)
	require.Same(t, reality, c.Inbounds[1].Extensions.Reality)
}

func TestServerConfig_Validate(t *testing.T) {
	t.Parallel()

	cfg := xray.DefaultServerConfig()
	require.NoError(t, cfg.Validate())

	cfg.Inbounds = slices.Repeat(cfg.Inbounds[:1], 17)
	require.ErrorIs(t, cfg.Validate(), validate.ErrInvalidConfig)
}

func dialFake(t *testing.T, h xraypb.HandlerServiceServer, s xraypb.StatsServiceServer) grpc.ClientConnInterface {
	t.Helper()

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	xraypb.RegisterHandlerServiceServer(srv, h)
	xraypb.RegisterStatsServiceServer(srv, s)

	serve := func() { _ = srv.Serve(lis) }
	go serve()

	t.Cleanup(srv.Stop)

	dial := func(context.Context, string) (net.Conn, error) { return lis.Dial() }

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(dial),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	closeConn := func() { _ = conn.Close() }
	t.Cleanup(closeConn)

	return conn
}

func injectionVectors() []string {
	return []string{
		"\n",
		"\r\n",
		"\x00",
		`"`,
		"'",
		"]",
		"\n[Interface]\nPostUp = id\n",
		"\nscript-security 2\nup /bin/sh",
		"\nsocks5:\n  listen: 0.0.0.0:1080",
		`"},{"listen":"0.0.0.0"`,
	}
}

func realityFixture() *xray.Reality {
	return &xray.Reality{
		Fingerprint: "chrome",
		PublicKey:   "-_v7-_v7-_v7-_v7-_v7-_v7-_v7-_v7-_v7-_v7-_s",
		ServerName:  "example.com",
		ShortID:     "ab",
	}
}
