package v2ray_test

import (
	"context"
	"errors"
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
	"github.com/sentinel-official/sentinel-go-sdk/v3/v2ray"
	"github.com/sentinel-official/sentinel-go-sdk/v3/v2ray/v2raypb"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
)

const bufSize = 1 << 20

type fakeHandler struct {
	v2raypb.UnimplementedHandlerServiceServer

	altered   []*v2raypb.AlterInboundRequest
	rejectTag string
}

func (h *fakeHandler) AlterInbound(
	_ context.Context,
	req *v2raypb.AlterInboundRequest,
) (*v2raypb.AlterInboundResponse, error) {
	if h.rejectTag != "" && req.GetTag() == h.rejectTag {
		return nil, errors.New("rejected")
	}

	h.altered = append(h.altered, req)

	return &v2raypb.AlterInboundResponse{}, nil
}

type fakeStats struct {
	v2raypb.UnimplementedStatsServiceServer

	stats map[string]int64
}

func (s *fakeStats) QueryStats(_ context.Context, req *v2raypb.QueryStatsRequest) (*v2raypb.QueryStatsResponse, error) {
	resp := &v2raypb.QueryStatsResponse{}

	for name, value := range s.stats {
		if req.GetPattern() == "" || strings.HasPrefix(name, req.GetPattern()) {
			resp.Stat = append(resp.Stat, &v2raypb.Stat{
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

	require.NoError(t, v2ray.API{}.AddUser(t.Context(), conn, "in-1", v2ray.ProtocolVLESS, "uuid-1"))
	require.Len(t, handler.altered, 1)

	req := handler.altered[0]
	require.Equal(t, "in-1", req.GetTag())
	require.Equal(t, "types.v2fly.org/v2ray.core.app.proxyman.command.AddUserOperation", req.GetOperation().GetTypeUrl())

	var op v2raypb.AddUserOperation
	require.NoError(t, proto.Unmarshal(req.GetOperation().GetValue(), &op))
	require.Equal(t, "uuid-1", op.GetUser().GetEmail())
	require.Equal(t, "types.v2fly.org/v2ray.core.proxy.vless.Account", op.GetUser().GetAccount().GetTypeUrl())

	var account v2raypb.VlessAccount
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

	out, err := v2ray.API{}.QueryStats(t.Context(), conn, "user>>>uuid-1>>>")
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

	require.NoError(t, v2ray.API{}.RemoveUser(t.Context(), conn, "in-1", "uuid-1"))
	require.Len(t, handler.altered, 1)

	req := handler.altered[0]
	require.Equal(t, "in-1", req.GetTag())
	require.Equal(
		t,
		"types.v2fly.org/v2ray.core.app.proxyman.command.RemoveUserOperation",
		req.GetOperation().GetTypeUrl(),
	)

	var op v2raypb.RemoveUserOperation
	require.NoError(t, proto.Unmarshal(req.GetOperation().GetValue(), &op))
	require.Equal(t, "uuid-1", op.GetEmail())
}

func TestCheckVersion(t *testing.T) {
	t.Parallel()

	const v4 = "V2Ray 4.34.0 (V2Fly, a community-driven edition of V2Ray.) Custom (go1.15 linux/amd64)\n"

	check := func(detail string, severity service.Severity) []service.PreflightCheck {
		return []service.PreflightCheck{service.NewPreflightCheck("service_version", severity, detail)}
	}

	cases := []struct {
		err      error
		name     string
		out      string
		severity service.Severity
		want     []service.PreflightCheck
	}{
		{
			name:     "v4 server",
			out:      v4,
			severity: service.SeverityError,
			want:     check("V2Ray 4.34.0 ignores run -config; install V2Ray 5", service.SeverityError),
		},
		{
			name:     "v4 client",
			out:      v4,
			severity: service.SeverityWarning,
			want:     check("V2Ray 4.34.0 ignores run -config; install V2Ray 5", service.SeverityWarning),
		},
		{
			err:      errors.New("exit status 2"),
			name:     "v5 rejects -version",
			severity: service.SeverityError,
		},
		{
			err:      context.DeadlineExceeded,
			name:     "hang",
			severity: service.SeverityError,
			want:     check("v2ray -version did not exit within 5s", service.SeverityWarning),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, v2ray.CheckVersion([]byte(tc.out), tc.err, tc.severity))
		})
	}
}

func TestErrorFromChecks(t *testing.T) {
	t.Parallel()

	const v4 = "V2Ray 4.34.0 (V2Fly, a community-driven edition of V2Ray.) Custom (go1.15 linux/amd64)\n"

	err := v2ray.ErrorFromChecks(v2ray.CheckVersion([]byte(v4), nil, service.SeverityError))
	require.EqualError(t, err, "V2Ray 4.34.0 ignores run -config; install V2Ray 5")

	require.NoError(t, v2ray.ErrorFromChecks(v2ray.CheckVersion(nil, errors.New("exit status 2"), service.SeverityError)))
	require.NoError(t, v2ray.ErrorFromChecks(v2ray.CheckVersion(nil, context.DeadlineExceeded, service.SeverityError)))
}

func TestExtensions_Validate(t *testing.T) {
	t.Parallel()

	ext := v2ray.Extensions{}
	require.NoError(t, ext.Validate(v2raycore.Stream{Security: v2ray.SecurityTLS}))
	require.ErrorIs(t, ext.Validate(v2raycore.Stream{Security: "reality"}), service.ErrInvalidPeer)
	require.ErrorIs(t, ext.Validate(v2raycore.Stream{Flow: "xtls-rprx-vision"}), service.ErrInvalidPeer)
}

func TestNewClient_Registered(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"v2ray"}, service.Binaries(v2ray.Name))
	sdktest.RequireClientRegistered(t, v2ray.Name, 1<<1, v2ray.ClientConfig{})

	dir, err := service.ClientToolDir(v2ray.Name)
	require.NoError(t, err)
	require.Equal(t, "/usr/local/etc/v2ray/sentinel-dvpn", dir)
}

func TestServerConfig_Validate(t *testing.T) {
	t.Parallel()

	cfg := v2ray.DefaultServerConfig()
	require.NoError(t, cfg.Validate())

	cfg.Inbounds = slices.Repeat(cfg.Inbounds[:1], 17)
	require.ErrorIs(t, cfg.Validate(), validate.ErrInvalidConfig)
}

func dialFake(t *testing.T, h v2raypb.HandlerServiceServer, s v2raypb.StatsServiceServer) grpc.ClientConnInterface {
	t.Helper()

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	v2raypb.RegisterHandlerServiceServer(srv, h)
	v2raypb.RegisterStatsServiceServer(srv, s)

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
