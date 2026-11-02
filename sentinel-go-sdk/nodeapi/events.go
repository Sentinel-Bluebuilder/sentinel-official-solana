package nodeapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
)

const defaultWriteTimeout = 10 * time.Second

type Event struct {
	Closed         *ClosedEvent
	VoucherRequest *VoucherRequestEvent
}

type EventsResponse struct {
	Body         io.Reader
	WriteTimeout time.Duration
}

func (response EventsResponse) VisitGetLeaseEventsResponse(w http.ResponseWriter) error {
	return response.write(w)
}

func (response EventsResponse) VisitGetSessionEventsResponse(w http.ResponseWriter) error {
	return response.write(w)
}

func (response EventsResponse) write(w http.ResponseWriter) (resultErr error) {
	if closer, ok := response.Body.(io.Closer); ok {
		defer func() { _ = closer.Close() }()
	}

	timeout := response.WriteTimeout
	if timeout <= 0 {
		timeout = defaultWriteTimeout
	}

	controller := http.NewResponseController(w)

	defer func() {
		if resultErr == nil {
			_ = controller.SetWriteDeadline(time.Time{})
		}
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	flush := func(chunk []byte) error {
		err := controller.SetWriteDeadline(time.Now().Add(timeout))
		if err != nil && !errors.Is(err, http.ErrNotSupported) {
			return fmt.Errorf("setting event stream deadline: %w", err)
		}

		if len(chunk) > 0 {
			if _, err := w.Write(chunk); err != nil {
				return fmt.Errorf("writing event stream: %w", err)
			}
		}

		if err := controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return fmt.Errorf("flushing event stream: %w", err)
		}

		if err := controller.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return fmt.Errorf("clearing event stream deadline: %w", err)
		}

		return nil
	}

	if err := flush(nil); err != nil {
		return err
	}

	buffer := make([]byte, 4096)
	for {
		n, err := response.Body.Read(buffer)
		if n > 0 {
			if flushErr := flush(buffer[:n]); flushErr != nil {
				return flushErr
			}
		}

		if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("reading event stream: %w", err)
		}
	}
}

func DecodeEvent(frame api.Event) (Event, error) {
	var event Event

	if frame.IsComment {
		return event, nil
	}

	var (
		schema string
		target any
	)

	switch frame.Name {
	case EventClosed:
		event.Closed = &ClosedEvent{}
		schema, target = "ClosedEvent", event.Closed
	case EventVoucherRequest:
		event.VoucherRequest = &VoucherRequestEvent{}
		schema, target = "VoucherRequestEvent", event.VoucherRequest
	default:
		return Event{}, nil
	}

	err := api.ValidateJSON(spec().Components.Schemas[schema].Value, frame.Data)
	if err == nil {
		err = json.Unmarshal(frame.Data, target)
	}

	if err != nil {
		return Event{}, fmt.Errorf("%w: %s event: %w", api.ErrInvalidResponse, frame.Name, err)
	}

	return event, nil
}
