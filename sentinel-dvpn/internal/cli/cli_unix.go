//go:build unix

package cli

import (
	"context"
	"fmt"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

func dialEndpoint(_ context.Context, endpoint string) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(
		"unix://"+endpoint,
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

// preflightEndpoint proves the daemon socket is live with a short connect, so a down daemon fails
// fast (errDaemonNotRunning) rather than blocking the lazy grpc dial's first call until its deadline.
func preflightEndpoint(_ context.Context, endpoint string) error {
	c, err := net.DialTimeout("unix", endpoint, preflightDialTimeout)
	if err != nil {
		return fmt.Errorf("%w: %w", errDaemonNotRunning, err)
	}
	_ = c.Close()

	return nil
}
