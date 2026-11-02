package api_test

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
)

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

type recordingReader struct {
	bytesRead int
	reader    io.Reader
}

func (r *recordingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.bytesRead += n

	if err != nil {
		return n, fmt.Errorf("reading test input: %w", err)
	}

	return n, nil
}

func TestEncodeEvent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		data string
		name string
		want string
	}{
		{data: `{"a":1}`, name: "compact json", want: "event: voucher_request\ndata: {\"a\":1}\n\n"},
		{data: "a\nb\n", name: "multiline", want: "event: voucher_request\ndata: a\ndata: b\ndata: \n\n"},
		{data: "", name: "empty", want: "event: voucher_request\ndata: \n\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, string(api.EncodeEvent(api.Event{Name: "voucher_request", Data: []byte(tc.data)})))
		})
	}
}

func TestEventComment(t *testing.T) {
	t.Parallel()

	require.Equal(t, ": ping\n\n", string(api.EventComment("ping")))
}

func TestEventDecoder_Next(t *testing.T) {
	t.Parallel()

	input := ": ping\n\n" + "event: voucher_request\ndata: {\"a\":1}\n\n" +
		"event: closed\ndata: {\"reason\":\"idle\"}\n\n"
	decoder := api.NewEventDecoder(strings.NewReader(input))

	for _, want := range []api.Event{
		{IsComment: true},
		{Name: "voucher_request", Data: []byte(`{"a":1}`)},
		{Name: "closed", Data: []byte(`{"reason":"idle"}`)},
	} {
		event, err := decoder.Next()
		require.NoError(t, err)
		require.Equal(t, want, event)
	}

	_, err := decoder.Next()
	require.ErrorIs(t, err, io.EOF)
}

func TestEventDecoder_Next_Fields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input string
		name  string
		want  api.Event
	}{
		{input: "data: a\ndata: b\n\n", name: "multiline", want: api.Event{Data: []byte("a\nb")}},
		{input: "data: a\r\ndata: b\r\n\r\n", name: "crlf", want: api.Event{Data: []byte("a\nb")}},
		{input: ": ping\ndata:\n\n", name: "empty data", want: api.Event{Data: []byte{}}},
		{input: ": ping\nevent: named\n\n", name: "named comment", want: api.Event{IsComment: true}},
		{input: "data:  value\n\n", name: "leading space", want: api.Event{Data: []byte(" value")}},
		{input: "\nretry: 5\n\ndata:value\n\n", name: "ignored frames", want: api.Event{Data: []byte("value")}},
		{
			input: "event: old\nevent: new\ndata: value\n\n", name: "last name",
			want: api.Event{Name: "new", Data: []byte("value")},
		},
		{input: "event: closed\n\ndata: next\n\n", name: "name without data", want: api.Event{Data: []byte("next")}},
		{input: "data\ndata: b\n\n", name: "bare data", want: api.Event{Data: []byte("\nb")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			event, err := api.NewEventDecoder(strings.NewReader(tc.input)).Next()
			require.NoError(t, err)
			require.Equal(t, tc.want, event)
		})
	}
}

func TestEventDecoder_Next_LongData(t *testing.T) {
	t.Parallel()

	data := strings.Repeat("a", 8*1024)
	event, err := api.NewEventDecoder(strings.NewReader("data: " + data + "\n\n")).Next()
	require.NoError(t, err)
	require.Equal(t, data, string(event.Data))
}

func TestEventDecoder_Next_ReadError(t *testing.T) {
	t.Parallel()

	_, err := api.NewEventDecoder(failingReader{}).Next()
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestEventDecoder_Next_RejectsOversizedBufferedLine(t *testing.T) {
	t.Parallel()

	input := "data: " + strings.Repeat("a", 32*1024) + "\n\n"
	reader := bufio.NewReaderSize(strings.NewReader(input), 64*1024)
	_, err := api.NewEventDecoder(reader).Next()

	require.ErrorIs(t, err, api.ErrResponseTooLarge)
}

func TestEventDecoder_Next_RejectsOversizedInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input string
		name  string
	}{
		{
			input: "data: " + strings.Repeat("a", 1<<20),
			name:  "line",
		},
		{
			input: strings.Repeat("data: "+strings.Repeat("a", 8*1024)+"\n", 128),
			name:  "frame",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reader := &recordingReader{reader: strings.NewReader(tc.input)}
			_, err := api.NewEventDecoder(reader).Next()

			require.ErrorIs(t, err, api.ErrResponseTooLarge)
			require.Less(t, reader.bytesRead, 128*1024)
		})
	}
}

func TestEventDecoder_Next_Unterminated(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"data: incomplete", "data: incomplete\n", "event: closed\n", ": ping\n"} {
		_, err := api.NewEventDecoder(strings.NewReader(input)).Next()
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	}
}
