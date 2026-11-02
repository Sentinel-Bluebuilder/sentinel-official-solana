package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/solana-foundation/solana-go/v2"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
)

type fakeStream struct {
	*io.PipeReader

	cancel context.CancelFunc
}

func (s *fakeStream) Close() error {
	s.cancel()

	if err := s.PipeReader.Close(); err != nil {
		return fmt.Errorf("closing fake stream: %w", err)
	}

	return nil
}

type fakeSubscriber struct {
	closed    bool
	mu        sync.Mutex
	notify    chan struct{}
	queue     [][]byte
	sessionID string
}

func (s *fakeSubscriber) enqueue(data []byte, closed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}

	s.queue = append(s.queue, data)
	s.closed = closed

	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *fakeSubscriber) take() ([][]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	queue := s.queue
	s.queue = nil

	return queue, s.closed
}

func (f *FakeNodeService) GetLeaseEvents(
	ctx context.Context,
	req nodeapi.GetLeaseEventsRequestObject,
) (nodeapi.GetLeaseEventsResponseObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	lease := f.lease(req.Lease)
	if signer, signed := api.SignerFrom(ctx); signed && lease.signer != nil && lease.signer.PublicKey() != signer {
		return nil, api.ErrUnauthorized
	}

	if lease.ledger.Status != nodeapi.LedgerStatusOpen {
		return nil, api.ErrChannelNotOpen
	}

	if f.leaseEventsJSON {
		return nodeapi.GetLeaseEventsdefaultJSONResponse{StatusCode: http.StatusOK}, nil
	}

	return nodeapi.GetLeaseEvents200TextEventStreamResponse{Body: f.openStream(ctx, req.Lease, "")}, nil
}

func (f *FakeNodeService) GetSessionEvents(
	ctx context.Context,
	req nodeapi.GetSessionEventsRequestObject,
) (nodeapi.GetSessionEventsResponseObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	sess, err := f.session(ctx, req.ID)
	if err != nil {
		return nil, err
	}

	id := req.ID
	if id == "" {
		id = f.currentSessionID
	}

	body := f.openStream(ctx, solana.PublicKey{}, id)
	if sess.status != nodeapi.SessionStatusActive {
		f.publishClosed(id, string(sess.closeReason))
	}

	return nodeapi.GetSessionEvents200TextEventStreamResponse{Body: body}, nil
}

func (f *FakeNodeService) PublishClosed(reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if sess := f.sessions[f.currentSessionID]; sess != nil {
		f.settleSession(sess, f.currentSessionID, reason)
	}

	for subscriber, address := range f.subscribers {
		if !address.IsZero() {
			subscriber.enqueue(closedFrame(reason), true)
		}
	}
}

func (f *FakeNodeService) PublishVoucherRequest() {
	f.mu.Lock()
	defer f.mu.Unlock()

	for subscriber, address := range f.subscribers {
		position := f.channelView()
		if !address.IsZero() {
			position = f.lease(address).position()
		}

		data, err := json.Marshal(nodeapi.VoucherRequestEvent(position))
		if err != nil {
			panic(err)
		}

		subscriber.enqueue(api.EncodeEvent(api.Event{
			Data: data,
			Name: nodeapi.EventVoucherRequest,
		}), false)
	}
}

func (f *FakeNodeService) openStream(ctx context.Context, address solana.PublicKey, sessionID string) io.Reader {
	ctx, cancel := context.WithCancel(ctx)
	reader, writer := io.Pipe()
	subscriber := &fakeSubscriber{
		notify:    make(chan struct{}, 1),
		sessionID: sessionID,
	}
	f.subscribers[subscriber] = address
	f.streamOpens++
	interval := f.heartbeatInterval

	go func() {
		defer cancel()

		f.writeStream(ctx, writer, subscriber, interval)
	}()

	return &fakeStream{
		PipeReader: reader,
		cancel:     cancel,
	}
}

func (f *FakeNodeService) writeStream(
	ctx context.Context,
	writer *io.PipeWriter,
	subscriber *fakeSubscriber,
	interval time.Duration,
) {
	stop := context.AfterFunc(ctx, func() { _ = writer.CloseWithError(ctx.Err()) })
	defer stop()
	defer func() { _ = writer.CloseWithError(ctx.Err()) }()
	defer func() {
		f.mu.Lock()
		delete(f.subscribers, subscriber)
		f.mu.Unlock()
	}()

	var (
		heartbeat <-chan time.Time
		timer     *time.Timer
	)

	if interval > 0 {
		timer = time.NewTimer(interval)
		defer timer.Stop()

		heartbeat = timer.C
	}

	for {
		queue, closed := subscriber.take()
		for _, data := range queue {
			if _, err := writer.Write(data); err != nil {
				return
			}

			if timer != nil {
				timer.Reset(interval)
			}
		}

		if closed {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-subscriber.notify:
		case <-heartbeat:
			if _, err := writer.Write(api.EventComment("heartbeat")); err != nil {
				return
			}

			timer.Reset(interval)
		}
	}
}

func closedFrame(reason string) []byte {
	data, err := json.Marshal(nodeapi.ClosedEvent{Reason: nodeapi.ClosedReason(reason)})
	if err != nil {
		panic(err)
	}

	return api.EncodeEvent(api.Event{
		Data: data,
		Name: nodeapi.EventClosed,
	})
}
