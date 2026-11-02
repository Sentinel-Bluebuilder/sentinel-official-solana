package nodeapi_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
)

const (
	headerTimeout       = 500 * time.Millisecond
	stalledWriteTimeout = 20 * time.Millisecond
	stopTimeout         = time.Second
)

func TestDecodeEvent(t *testing.T) {
	t.Parallel()

	voucher := func(owed string) []byte {
		return []byte(`{"consumption":{"owed_base_units":` + owed + `,"settled_base_units":0,` +
			`"usage":{"rx_bytes":1,"seconds":2,"tx_bytes":3}},` +
			`"ledger":{"basis":"per_gb","cumulative_base_units":0,"escrow_base_units":10,"opened_at":1,` +
			`"price":5,"redeemed_base_units":0,"status":"open"}}`)
	}

	cases := []struct {
		check func(*testing.T, nodeapi.Event)
		err   error
		frame api.Event
		name  string
	}{
		{
			check: func(t *testing.T, event nodeapi.Event) {
				t.Helper()
				require.NotNil(t, event.Closed)
				require.Equal(t, nodeapi.ClosedReason(nodeapi.ReasonIdle), event.Closed.Reason)
			},
			frame: api.Event{Name: nodeapi.EventClosed, Data: []byte(`{"reason":"idle"}`)},
			name:  "closed",
		},
		{
			err:   api.ErrInvalidResponse,
			frame: api.Event{Name: nodeapi.EventClosed, Data: []byte(`{"reason":"idle","REASON":"\u001b[31mpwned\nline2"}`)},
			name:  "closed case variant key",
		},
		{
			err:   api.ErrInvalidResponse,
			frame: api.Event{Name: nodeapi.EventClosed, Data: []byte(`{"reason":"boom"}`)},
			name:  "closed unknown reason",
		},
		{
			check: func(t *testing.T, event nodeapi.Event) {
				t.Helper()
				require.NotNil(t, event.VoucherRequest)
				require.Equal(t, uint64(7), event.VoucherRequest.Consumption.OwedBaseUnits)
			},
			frame: api.Event{Name: nodeapi.EventVoucherRequest, Data: voucher("7")},
			name:  "voucher request",
		},
		{
			err:   api.ErrInvalidResponse,
			frame: api.Event{Name: nodeapi.EventVoucherRequest, Data: voucher("-1")},
			name:  "voucher request negative owed",
		},
		{
			err:   api.ErrInvalidResponse,
			frame: api.Event{Name: nodeapi.EventClosed, Data: []byte(`{`)},
			name:  "malformed json",
		},
		{
			check: func(t *testing.T, event nodeapi.Event) {
				t.Helper()
				require.Equal(t, nodeapi.Event{}, event)
			},
			frame: api.Event{Name: "mystery", Data: []byte(`{}`)},
			name:  "unknown name",
		},
		{
			check: func(t *testing.T, event nodeapi.Event) {
				t.Helper()
				require.Equal(t, nodeapi.Event{}, event)
			},
			frame: api.Event{IsComment: true, Data: []byte("ping")},
			name:  "comment",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			event, err := nodeapi.DecodeEvent(tc.frame)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.Equal(t, nodeapi.Event{}, event)

				return
			}

			require.NoError(t, err)
			tc.check(t, event)
		})
	}
}

func TestDecodeEvent_BoundsErrorText(t *testing.T) {
	t.Parallel()

	data := []byte(`{"reason":"` + strings.Repeat(`\u001b`, 100000) + `"}`)

	_, err := nodeapi.DecodeEvent(api.Event{Name: nodeapi.EventClosed, Data: data})

	require.ErrorIs(t, err, api.ErrInvalidResponse)
	require.LessOrEqual(t, len(err.Error()), 400)
	require.NotContains(t, err.Error(), "\x1b")
}

func TestEventsResponse_VisitGetSessionEventsResponse_AllowsIdleHTTP2(t *testing.T) {
	t.Parallel()

	const writeTimeout = 25 * time.Millisecond

	reader, writer := io.Pipe()
	finished := make(chan error, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		response := nodeapi.EventsResponse{Body: reader, WriteTimeout: writeTimeout}
		finished <- response.VisitGetSessionEventsResponse(w)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })

	writes := make(chan error, 1)

	go func() {
		_, err := io.WriteString(writer, "data: first\n\n")
		writes <- err
	}()

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	require.Equal(t, 2, response.ProtoMajor)

	decoder := api.NewEventDecoder(response.Body)
	frame, err := decoder.Next()
	require.NoError(t, err)
	require.Equal(t, []byte("first"), frame.Data)
	require.NoError(t, <-writes)

	timer := time.NewTimer(4 * writeTimeout)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatal("idle stream test canceled")
	}

	go func() {
		_, err := io.WriteString(writer, "data: second\n\n")
		writes <- err
	}()

	frame, err = decoder.Next()
	require.NoError(t, err)
	require.Equal(t, []byte("second"), frame.Data)
	require.NoError(t, <-writes)
	require.NoError(t, writer.Close())

	_, err = decoder.Next()
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, <-finished)
}

func TestEventsResponse_VisitGetSessionEventsResponse_EndsCleanlyOnCancelledRead(t *testing.T) {
	t.Parallel()

	body := io.MultiReader(strings.NewReader("data: first\n\n"), iotest.ErrReader(context.Canceled))
	response := nodeapi.EventsResponse{Body: body}
	recorder := httptest.NewRecorder()

	require.NoError(t, response.VisitGetSessionEventsResponse(recorder))
	require.Equal(t, "data: first\n\n", recorder.Body.String())
}

func TestEventsResponse_VisitGetSessionEventsResponse_FlushesHeadersBeforeFirstEvent(t *testing.T) {
	t.Parallel()

	reader, writer := io.Pipe()
	finished := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		response := nodeapi.EventsResponse{Body: reader}
		finished <- response.VisitGetSessionEventsResponse(w)
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { _ = writer.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), headerTimeout)
	t.Cleanup(cancel)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	require.NoError(t, err)

	resp, err := server.Client().Do(request)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, writer.Close())

	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(stopTimeout):
		t.Fatal("handler did not stop after the pipe closed")
	}
}

func TestEventsResponse_VisitGetSessionEventsResponse_StopsStalledReader(t *testing.T) {
	t.Parallel()

	finished := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body := strings.NewReader(strings.Repeat("data: stalled\n\n", 1<<20))

		response := nodeapi.EventsResponse{Body: body, WriteTimeout: stalledWriteTimeout}
		finished <- response.VisitGetSessionEventsResponse(w)
	}))
	t.Cleanup(srv.Close)

	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", srv.Listener.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n")
	require.NoError(t, err)

	select {
	case err := <-finished:
		var timeout net.Error
		require.ErrorAs(t, err, &timeout)
		require.True(t, timeout.Timeout())
	case <-time.After(stopTimeout):
		t.Fatal("stream write remained blocked")
	}
}
