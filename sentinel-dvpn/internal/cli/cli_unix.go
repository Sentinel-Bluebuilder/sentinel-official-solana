//go:build unix

package cli

import (
	"context"
	"fmt"

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
