package api

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	maxEventBytes     = 64 * 1024
	maxEventLineBytes = 16 * 1024
)

type Event struct {
	Data      []byte
	IsComment bool
	Name      string
}

type EventDecoder struct {
	reader *bufio.Reader
}

func NewEventDecoder(r io.Reader) *EventDecoder {
	reader := struct{ io.Reader }{Reader: r}

	return &EventDecoder{reader: bufio.NewReaderSize(reader, maxEventLineBytes)}
}

func (d *EventDecoder) Next() (Event, error) {
	var event Event

	hasComment, hasData := false, false
	frameBytes := 0

	for {
		raw, err := d.reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			return Event{}, fmt.Errorf("%w: event line over %d bytes", ErrResponseTooLarge, maxEventLineBytes)
		}

		frameBytes += len(raw)
		if frameBytes > maxEventBytes {
			return Event{}, fmt.Errorf("%w: event over %d bytes", ErrResponseTooLarge, maxEventBytes)
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				if frameBytes > 0 {
					return Event{}, io.ErrUnexpectedEOF
				}

				return Event{}, io.EOF
			}

			return Event{}, fmt.Errorf("reading event stream: %w", err)
		}

		line := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
		if line == "" {
			if hasData || hasComment {
				if !hasData {
					event = Event{IsComment: true}
				}

				return event, nil
			}

			event = Event{}
			frameBytes = 0

			continue
		}

		if strings.HasPrefix(line, ":") {
			hasComment = true

			continue
		}

		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")

		switch field {
		case "event":
			event.Name = value
		case "data":
			if hasData {
				event.Data = append(event.Data, '\n')
			} else {
				event.Data = []byte{}
			}

			event.Data = append(event.Data, value...)
			hasData = true
		}
	}
}

func EncodeEvent(event Event) []byte {
	result := []byte("event: " + event.Name + "\n")
	for part := range bytes.SplitSeq(event.Data, []byte{'\n'}) {
		result = append(result, "data: "...)
		result = append(result, part...)
		result = append(result, '\n')
	}

	return append(result, '\n')
}

func EventComment(text string) []byte {
	return []byte(": " + text + "\n\n")
}
