package client

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
)

type nodeEvent struct {
	closed    bool
	err       error
	heartbeat bool
	reason    string
	request   *nodeapi.VoucherRequestEvent
}

func (r *Runtime) readEvents(ctx context.Context, conn *connection, events chan<- nodeEvent) {
	backoff := minStreamBackoff

	for ctx.Err() == nil {
		received, err := r.readStream(ctx, conn, events)
		if err != nil {
			select {
			case <-ctx.Done():
			case events <- nodeEvent{err: err}:
			}

			return
		}

		if received {
			backoff = minStreamBackoff
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()

			return
		case <-timer.C:
		}

		backoff = min(2*backoff, maxStreamBackoff)
	}
}

func (r *Runtime) readStream(ctx context.Context, conn *connection, events chan<- nodeEvent) (bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	timeout := missedHeartbeats * conn.heartbeat

	watchdog := time.AfterFunc(timeout, cancel)
	defer watchdog.Stop()

	response, err := conn.streamClient.GetSessionEvents(ctx, conn.session.ID)
	if isInvalidResponse(err) {
		return false, err
	}

	if err != nil {
		return false, nil
	}

	defer func() { _ = response.Body.Close() }()

	switch response.StatusCode {
	case http.StatusUnauthorized:
		return false, api.ErrUnauthorized
	case http.StatusNotFound:
		return false, api.ErrSessionNotFound
	}

	if response.StatusCode != http.StatusOK {
		return false, nil
	}

	decoder := api.NewEventDecoder(response.Body)
	received := false

	for {
		frame, err := decoder.Next()
		if err != nil {
			return received, nil
		}

		event, err := decodeNodeEvent(frame)
		if err != nil {
			return false, err
		}

		received = true

		watchdog.Reset(timeout)

		select {
		case <-ctx.Done():
			return received, nil
		case events <- event:
		}
	}
}

func decodeNodeEvent(frame api.Event) (nodeEvent, error) {
	decoded, err := nodeapi.DecodeEvent(frame)
	if err != nil {
		return nodeEvent{}, fmt.Errorf("decoding node event: %w", err)
	}

	event := nodeEvent{
		heartbeat: true,
		request:   decoded.VoucherRequest,
	}
	if decoded.Closed != nil {
		event.closed = true
		event.reason = string(decoded.Closed.Reason)
	}

	return event, nil
}
