package api_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
)

func TestValidatedTransport_RoundTrip(t *testing.T) {
	t.Parallel()

	spec, err := openapi3.NewLoader().LoadFromFile("testdata/validated_transport.yaml")
	require.NoError(t, err)

	frame := "data: " + strings.Repeat("a", 32) + "\n\n"
	name := func(size int) string { return `{"name":"` + strings.Repeat("a", size-11) + `"}` }
	cases := []struct {
		err      error
		name     string
		path     string
		response fakeResponse
	}{
		{name: "valid json", path: "/v1/thing", response: jsonResponse(http.StatusOK, `{"name":"x"}`)},
		{
			name: "json with charset parameter",
			path: "/v1/thing",
			response: fakeResponse{
				body:        `{"name":"x"}`,
				contentType: "application/json; charset=utf-8",
				status:      http.StatusOK,
			},
		},
		{
			err:      api.ErrInvalidResponse,
			name:     "body off schema",
			path:     "/v1/thing",
			response: jsonResponse(http.StatusOK, `{"name":1}`),
		},
		{
			err:      api.ErrUnexpectedRedirect,
			name:     "redirect",
			path:     "/v1/thing",
			response: fakeResponse{location: "/v1/stream", status: http.StatusFound},
		},
		{name: "body of exactly max bytes", path: "/v1/thing", response: jsonResponse(http.StatusOK, name(64))},
		{
			err:      api.ErrResponseTooLarge,
			name:     "body one byte over max bytes",
			path:     "/v1/thing",
			response: jsonResponse(http.StatusOK, name(65)),
		},
		{name: "no content", path: "/v1/thing", response: fakeResponse{status: http.StatusNoContent}},
		{
			name:     "valid error body",
			path:     "/v1/thing",
			response: jsonResponse(http.StatusServiceUnavailable, `{"error":{"code":"internal","message":"boom"}}`),
		},
		{
			err:      api.ErrInvalidResponse,
			name:     "error message over max length",
			path:     "/v1/thing",
			response: jsonResponse(http.StatusServiceUnavailable, `{"error":{"code":"x","message":"123456789"}}`),
		},
		{
			name:     "event stream over max bytes",
			path:     "/v1/stream",
			response: fakeResponse{body: frame + frame, contentType: "text/event-stream", status: http.StatusOK},
		},
		{
			err:      api.ErrResponseTooLarge,
			name:     "event stream with error status",
			path:     "/v1/stream",
			response: fakeResponse{body: frame + frame, contentType: "text/event-stream", status: http.StatusServiceUnavailable},
		},
		{
			err:      api.ErrInvalidResponse,
			name:     "json on event stream route",
			path:     "/v1/stream",
			response: jsonResponse(http.StatusOK, `{"name":"x"}`),
		},
		{
			err:      api.ErrInvalidResponse,
			name:     "event stream on json route",
			path:     "/v1/thing",
			response: fakeResponse{body: frame, contentType: "text/event-stream", status: http.StatusOK},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, url := startFakeServer(t, tc.response)
			client := &http.Client{Transport: api.NewValidatedTransport(http.DefaultTransport, spec, 64)}

			resp, err := get(t.Context(), client, url+tc.path)

			require.Len(t, srv.received(), 1)

			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.Nil(t, resp)

				return
			}

			require.NoError(t, err)

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, tc.response.status, resp.StatusCode)
			require.Equal(t, tc.response.body, string(body))
		})
	}
}

func TestValidatedTransport_RoundTrip_BoundsErrorText(t *testing.T) {
	t.Parallel()

	spec, err := openapi3.NewLoader().LoadFromFile("testdata/validated_transport.yaml")
	require.NoError(t, err)

	body := `{"name":["` + strings.Repeat(`\u001b`, 100000) + `"]}`
	_, url := startFakeServer(t, jsonResponse(http.StatusOK, body))
	transport := api.NewValidatedTransport(http.DefaultTransport, spec, api.MaxResponseBodyBytes)
	client := &http.Client{Transport: transport}

	resp, err := get(t.Context(), client, url+"/v1/thing")
	if resp != nil {
		require.NoError(t, resp.Body.Close())
	}

	require.ErrorIs(t, err, api.ErrInvalidResponse)
	require.LessOrEqual(t, len(err.Error()), 400)
	require.NotContains(t, err.Error(), "\x1b")
}

func TestValidatedTransport_RoundTrip_UnknownRoute(t *testing.T) {
	t.Parallel()

	spec, err := openapi3.NewLoader().LoadFromFile("testdata/validated_transport.yaml")
	require.NoError(t, err)

	srv, url := startFakeServer(t, jsonResponse(http.StatusOK, `{"name":"x"}`))
	client := &http.Client{Transport: api.NewValidatedTransport(http.DefaultTransport, spec, 64)}

	resp, err := get(t.Context(), client, url+"/v1/missing")
	if resp != nil {
		require.NoError(t, resp.Body.Close())
	}

	require.ErrorIs(t, err, routers.ErrPathNotFound)
	require.NotErrorIs(t, err, api.ErrInvalidResponse)
	require.Nil(t, resp)
	require.Empty(t, srv.received())
}

func jsonResponse(status int, body string) fakeResponse {
	return fakeResponse{
		body:        body,
		contentType: "application/json",
		status:      status,
	}
}
