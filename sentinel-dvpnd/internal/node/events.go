//go:build linux

package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
)

type eventHub struct {
	mu          sync.Mutex
	subscribers map[string]map[chan []byte]struct{}
}

func newEventHub() *eventHub {
	return &eventHub{subscribers: make(map[string]map[chan []byte]struct{})}
}

func (e *eventHub) publish(topic string, frame []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()

	terminal := isTerminalFrame(frame)

	for subscriber := range e.subscribers[topic] {
		select {
		case subscriber <- frame:
		default:
			if !terminal {
				continue
			}

			select {
			case <-subscriber:
			default:
			}

			subscriber <- frame
		}
	}
}

func (e *eventHub) subscribe(topic string) (<-chan []byte, func()) {
	subscriber := make(chan []byte, 8)

	e.mu.Lock()
	if e.subscribers[topic] == nil {
		e.subscribers[topic] = make(map[chan []byte]struct{})
	}

	e.subscribers[topic][subscriber] = struct{}{}
	e.mu.Unlock()

	unsubscribe := func() {
		e.mu.Lock()
		defer e.mu.Unlock()

		delete(e.subscribers[topic], subscriber)

		if len(e.subscribers[topic]) == 0 {
			delete(e.subscribers, topic)
		}
	}

	return subscriber, unsubscribe
}

type eventStream struct {
	io.ReadCloser

	cancel context.CancelFunc
}

func (s *eventStream) Close() error {
	s.cancel()

	if err := s.ReadCloser.Close(); err != nil {
		return fmt.Errorf("closing event stream: %w", err)
	}

	return nil
}

func (r *Runtime) openEventStream(ctx context.Context, topic string) io.Reader {
	ctx, cancel := context.WithCancel(ctx)
	pr, pw := io.Pipe()
	frames, unsubscribe := r.events.subscribe(topic)
	stop := context.AfterFunc(ctx, func() { _ = pw.CloseWithError(ctx.Err()) })

	go func() {
		defer cancel()
		defer unsubscribe()
		defer func() { _ = pw.Close() }()
		defer stop()

		timer := time.NewTimer(time.Duration(r.cfg.Sessions.HeartbeatSeconds) * time.Second)
		defer timer.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case frame := <-frames:
				if _, err := pw.Write(frame); err != nil {
					return
				}

				timer.Reset(time.Duration(r.cfg.Sessions.HeartbeatSeconds) * time.Second)

				if isTerminalFrame(frame) {
					return
				}
			case <-timer.C:
				if _, err := pw.Write(api.EventComment("heartbeat")); err != nil {
					return
				}

				timer.Reset(time.Duration(r.cfg.Sessions.HeartbeatSeconds) * time.Second)
			}
		}
	}()

	return &eventStream{
		ReadCloser: pr,
		cancel:     cancel,
	}
}

func (r *Runtime) publishClosed(topic, reason string) error {
	data, err := json.Marshal(nodeapi.ClosedEvent{Reason: nodeapi.ClosedReason(reason)})
	if err != nil {
		return fmt.Errorf("marshaling closed event: %w", err)
	}

	r.events.publish(topic, api.EncodeEvent(api.Event{
		Data: data,
		Name: nodeapi.EventClosed,
	}))

	return nil
}

func isTerminalFrame(frame []byte) bool {
	return bytes.HasPrefix(frame, []byte("event: "+nodeapi.EventClosed+"\n"))
}
