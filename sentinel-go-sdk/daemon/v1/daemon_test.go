package daemonv1_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
)

var errArbitrary = errors.New("test error")

type fakeDaemonService struct {
	daemonv1.UnimplementedDaemonServiceServer
}

func (fakeDaemonService) GetVersion(
	context.Context,
	*daemonv1.GetVersionRequest,
) (*daemonv1.GetVersionResponse, error) {
	version := &daemonv1.Version{
		Network: "test-network",
		Tag:     "v1.2.3",
	}

	return &daemonv1.GetVersionResponse{Version: version}, nil
}
func TestDaemonService_RoundTrip(t *testing.T) {
	t.Parallel()

	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	daemonv1.RegisterDaemonServiceServer(srv, fakeDaemonService{})

	serve := func() { _ = srv.Serve(lis) }
	go serve()

	t.Cleanup(srv.Stop)

	dial := func(context.Context, string) (net.Conn, error) { return lis.Dial() }

	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithContextDialer(dial),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	req := &daemonv1.GetVersionRequest{}

	resp, err := daemonv1.NewDaemonServiceClient(conn).GetVersion(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, "test-network", resp.GetVersion().GetNetwork())
}

func TestStatus_MapsErrorsToCodes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		code codes.Code
	}{
		{"not found", fmt.Errorf("loading: %w", daemonv1.ErrNotFound), codes.NotFound},
		{"canceled", context.Canceled, codes.Canceled},
		{"deadline", fmt.Errorf("waiting: %w", context.DeadlineExceeded), codes.DeadlineExceeded},
		{
			"invalid argument",
			fmt.Errorf("%w: parsing plan x: bad base58", daemonv1.ErrInvalidArgument),
			codes.InvalidArgument,
		},
		{"not found sentinel", fmt.Errorf("%w: no active session", daemonv1.ErrNotFound), codes.NotFound},
		{"failed precondition", fmt.Errorf("%w: node not live", daemonv1.ErrFailedPrecondition), codes.FailedPrecondition},
		{"unavailable", fmt.Errorf("%w: still recovering", daemonv1.ErrUnavailable), codes.Unavailable},
		{
			"failed precondition over not found",
			fmt.Errorf("%w: %w", daemonv1.ErrFailedPrecondition, fmt.Errorf("ticket x: %w", daemonv1.ErrNotFound)),
			codes.FailedPrecondition,
		},
		{
			"failed precondition over deadline",
			fmt.Errorf("%w: %w", daemonv1.ErrFailedPrecondition, fmt.Errorf("dialing node: %w", context.DeadlineExceeded)),
			codes.FailedPrecondition,
		},
		{"internal", errArbitrary, codes.Internal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := daemonv1.ToStatus(tc.err)
			require.Equal(t, tc.code, status.Code(got))
			require.Equal(t, tc.err.Error(), status.Convert(got).Message())
		})
	}
}
