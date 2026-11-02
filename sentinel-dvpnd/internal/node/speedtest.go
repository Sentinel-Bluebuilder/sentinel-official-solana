//go:build linux

package node

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/showwin/speedtest-go/speedtest"

	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
)

const (
	bytesPerMbps     = 125_000
	speedtestTimeout = 2 * time.Minute
)

var errMissingSpeedtestServers = errors.New("no servers available")

type speedtestResult struct {
	bandwidth nodeapi.Bandwidth
	err       error
}

func (r *Runtime) bandwidth() nodeapi.Bandwidth {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.speed
}

func (r *Runtime) runSpeedtest(ctx context.Context) error {
	callCtx, cancel := context.WithTimeout(ctx, speedtestTimeout)
	defer cancel()

	results := make(chan speedtestResult, 1)

	go func() {
		defer func() {
			if p := recover(); p != nil {
				results <- speedtestResult{err: fmt.Errorf("speed test panicked: %v", p)}
			}
		}()

		bandwidth, err := measureBandwidth(callCtx)
		results <- speedtestResult{
			bandwidth: bandwidth,
			err:       err,
		}
	}()

	select {
	case <-callCtx.Done():
		return fmt.Errorf("running speed test: %w", callCtx.Err())
	case result := <-results:
		if result.err != nil {
			return result.err
		}

		r.mu.Lock()
		r.speed = result.bandwidth
		r.mu.Unlock()

		return nil
	}
}

func measureBandwidth(ctx context.Context) (nodeapi.Bandwidth, error) {
	client := speedtest.New()

	servers, err := client.FetchServerListContext(ctx)
	if err != nil {
		return nodeapi.Bandwidth{}, fmt.Errorf("fetching speedtest servers: %w", err)
	}

	targets, err := servers.FindServer(nil)
	if err != nil {
		return nodeapi.Bandwidth{}, fmt.Errorf("finding speedtest server: %w", err)
	}

	if len(targets) == 0 {
		return nodeapi.Bandwidth{}, fmt.Errorf("finding speedtest server: %w", errMissingSpeedtestServers)
	}

	target := targets[0]
	if err := target.DownloadTestContext(ctx); err != nil {
		return nodeapi.Bandwidth{}, fmt.Errorf("running download test: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return nodeapi.Bandwidth{}, fmt.Errorf("running speed test: %w", err)
	}

	if err := target.UploadTestContext(ctx); err != nil {
		return nodeapi.Bandwidth{}, fmt.Errorf("running upload test: %w", err)
	}

	return nodeapi.Bandwidth{
		DownloadBytesPerSecond: uint64(target.DLSpeed.Mbps() * bytesPerMbps),
		UploadBytesPerSecond:   uint64(target.ULSpeed.Mbps() * bytesPerMbps),
	}, nil
}
