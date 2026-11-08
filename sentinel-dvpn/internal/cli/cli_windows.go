package cli

import (
	"context"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
)

const (
	pipeClientRights = windows.FILE_READ_DATA | windows.FILE_WRITE_DATA | windows.FILE_READ_ATTRIBUTES |
		windows.FILE_WRITE_ATTRIBUTES | windows.READ_CONTROL | windows.SYNCHRONIZE
	pipeTarget = "passthrough:///" + instance.DaemonName
)

func dialEndpoint(_ context.Context, endpoint string) (*grpc.ClientConn, error) {
	dial := func(ctx context.Context, _ string) (net.Conn, error) {
		return winio.DialPipeAccess(ctx, endpoint, pipeClientRights)
	}

	conn, err := grpc.NewClient(
		pipeTarget,
		grpc.WithContextDialer(dial),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			PermitWithoutStream: true,
			Time:                keepaliveTime,
			Timeout:             keepaliveTimeout,
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("creating grpc client: %w", err)
	}

	return conn, nil
}

// preflightEndpoint proves the daemon pipe is live with a short open, so a down daemon fails fast
// (errDaemonNotRunning) rather than blocking the lazy grpc dial's first call until its deadline.
func preflightEndpoint(ctx context.Context, endpoint string) error {
	pctx, cancel := context.WithTimeout(ctx, preflightDialTimeout)
	defer cancel()

	c, err := winio.DialPipeAccess(pctx, endpoint, pipeClientRights)
	if err != nil {
		return fmt.Errorf("%w: %w", errDaemonNotRunning, err)
	}
	_ = c.Close()

	return nil
}
