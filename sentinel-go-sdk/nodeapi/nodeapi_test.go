package nodeapi_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
	"github.com/sentinel-official/sentinel-go-sdk/v3/wireguard"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

type shoutedMetadata struct {
	nodeapi.Metadata

	ShoutedSDKVersion string `json:"SDK_VERSION"` //nolint:tagliatelle
}

func TestDiscover(t *testing.T) {
	t.Parallel()

	ownerKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	operatorKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	fakeRPC := sdktest.NewFakeRPC(t)
	seedGrant(t, fakeRPC, ownerKey.PublicKey(), operatorKey.PublicKey())

	programClient := program.NewClient(fakeRPC.NewClient(), dvpn.ProgramID, solana.PublicKey{}, rpc.CommitmentConfirmed)
	nodeAddress := solana.PublicKey{2}
	cases := []struct {
		err     error
		mutate  func(*nodeapi.Metadata)
		name    string
		payload func(nodeapi.Metadata) any
		uri     func(string) string
		uriLen  uint8
	}{
		{
			err:  api.ErrInvalidResponse,
			name: "case variant payload key",
			payload: func(m nodeapi.Metadata) any {
				return shoutedMetadata{Metadata: m, ShoutedSDKVersion: "\x1b[31mpwned"}
			},
		},
		{
			err:  api.ErrInvalidPeerURL,
			name: "empty uri",
			uri:  func(string) string { return "" },
		},
		{
			err:    api.ErrInvalidPeerURL,
			mutate: func(m *nodeapi.Metadata) { m.API.URLs = []string{"https://x:0"} },
			name:   "invalid api url",
		},
		{
			err:    api.ErrInvalidResponse,
			mutate: func(m *nodeapi.Metadata) { m.Settlement.HeartbeatSeconds = 0 },
			name:   "invalid metadata payload",
		},
		{
			err:  api.ErrInvalidPeerURL,
			name: "uri without scheme",
			uri:  func(raw string) string { return strings.TrimPrefix(raw, "https://") },
		},
		{
			err:    api.ErrInvalidPeerURL,
			name:   "uri length over the uri field",
			uriLen: 200,
		},
		{
			name: "valid",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			metadata := metadataFixture(nodeAddress, ownerKey.PublicKey(), operatorKey.PublicKey(), dvpn.ProgramID, time.Now())
			if tc.mutate != nil {
				tc.mutate(&metadata)
			}

			signed, err := nodeapi.SignMetadata(operatorKey, metadata)
			if tc.payload != nil {
				signed, err = auth.SignMessage(operatorKey, auth.DomainNodeMetadata, tc.payload(metadata))
			}

			require.NoError(t, err)

			srv := startSignedMetadataServer(t, signed)

			uri := srv.URL
			if tc.uri != nil {
				uri = tc.uri(srv.URL)
			}

			node := dvpn.Node{Owner: ownerKey.PublicKey(), URILen: uint8(len(uri))}
			copy(node.URI[:], uri)

			if tc.uriLen != 0 {
				node.URILen = tc.uriLen
			}

			got, url, err := nodeapi.Discover(t.Context(), programClient, nodeAddress, node, time.Second)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)

				return
			}

			require.NoError(t, err)

			want, err := api.ParsePeerURL(srv.URL)
			require.NoError(t, err)
			require.Equal(t, want, url)
			require.Equal(t, metadata.API, got.API)
		})
	}
}

func TestEventsResponse_VisitGetSessionEventsResponse_Flushes(t *testing.T) {
	t.Parallel()

	tests := map[string]func(http.ResponseWriter, io.Reader) error{
		"lease": func(w http.ResponseWriter, body io.Reader) error {
			response := nodeapi.EventsResponse{Body: body}

			return response.VisitGetLeaseEventsResponse(w)
		},
		"session": func(w http.ResponseWriter, body io.Reader) error {
			response := nodeapi.EventsResponse{Body: body}

			return response.VisitGetSessionEventsResponse(w)
		},
	}

	for name, visit := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reader, writer := io.Pipe()
			finished := make(chan error, 1)
			handler := func(w http.ResponseWriter, _ *http.Request) {
				finished <- visit(w, reader)
			}
			srv := httptest.NewServer(http.HandlerFunc(handler))
			t.Cleanup(srv.Close)
			t.Cleanup(func() { _ = writer.Close() })
			t.Cleanup(func() { _ = reader.Close() })

			const frame = "event: voucher_request\ndata: {}\n\n"

			written := make(chan error, 1)

			go func() {
				_, err := io.WriteString(writer, frame)
				written <- err
			}()

			client := &http.Client{Timeout: time.Second}
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
			require.NoError(t, err)

			response, err := client.Do(request)
			require.NoError(t, err)

			defer func() { _ = response.Body.Close() }()

			require.Equal(t, http.StatusOK, response.StatusCode)
			require.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
			require.Empty(t, response.Header.Get("Content-Length"))

			got := make([]byte, len(frame))
			_, err = io.ReadFull(response.Body, got)
			require.NoError(t, err)
			require.Equal(t, frame, string(got))
			require.NoError(t, <-written)

			select {
			case err := <-finished:
				t.Fatalf("stream completed before body closed: %v", err)
			default:
			}

			require.NoError(t, writer.Close())
			require.NoError(t, <-finished)
		})
	}
}

func TestGetSpec_ClosedReasons(t *testing.T) {
	t.Parallel()

	reasons := map[nodeapi.ClosedReason]string{
		nodeapi.ClosedReasonClient:          nodeapi.ReasonClient,
		nodeapi.ClosedReasonCloseGrace:      nodeapi.ReasonCloseGrace,
		nodeapi.ClosedReasonCreditExhausted: nodeapi.ReasonCreditExhausted,
		nodeapi.ClosedReasonDataCapReached:  nodeapi.ReasonDataCapReached,
		nodeapi.ClosedReasonIdle:            nodeapi.ReasonIdle,
		nodeapi.ClosedReasonLeaseNotFunded:  nodeapi.ReasonLeaseNotFunded,
		nodeapi.ClosedReasonNoEnforcer:      nodeapi.ReasonNoEnforcer,
		nodeapi.ClosedReasonOperator:        nodeapi.ReasonOperator,
		nodeapi.ClosedReasonPositionClosed:  nodeapi.ReasonPositionClosed,
		nodeapi.ClosedReasonPositionClosing: nodeapi.ReasonPositionClosing,
		nodeapi.ClosedReasonReplaced:        nodeapi.ReasonReplaced,
		nodeapi.ClosedReasonRetry:           nodeapi.ReasonRetry,
		nodeapi.ClosedReasonTicketExpired:   nodeapi.ReasonTicketExpired,
	}

	spec, err := nodeapi.GetSpec()
	require.NoError(t, err)

	enum := spec.Components.Schemas["ClosedEvent"].Value.Properties["reason"].Value.Enum

	values := make([]string, 0, len(enum))
	for _, value := range enum {
		values = append(values, value.(string))
	}

	constants := make([]string, 0, len(reasons))
	for reason, constant := range reasons {
		require.EqualValues(t, constant, reason)

		constants = append(constants, constant)
	}

	require.ElementsMatch(t, constants, values)
}

func TestGetSpec_Constraints(t *testing.T) {
	t.Parallel()

	const (
		openSession = `{"consumption":{"owed_base_units":0,"settled_base_units":0,` +
			`"usage":{"rx_bytes":0,"seconds":0,"tx_bytes":0}},` +
			`"peer":{"id":"a","payload":{},"type":"wireguard"},"session_id":%q}`
		settlement = `{"heartbeat_seconds":%d,"min_channel_prepay_bytes":1,"min_channel_prepay_seconds":1,` +
			`"min_lease_prepay_seconds":1,"prices":{"channel_price_per_gb":1,"channel_price_per_hour":1,` +
			`"lease_price_per_hour":1}}`
	)

	fingerprint := strings.Repeat("0", 64)
	key := strings.Repeat("A", 43) + "="
	cases := []struct {
		name   string
		ok     bool
		schema string
		value  string
	}{
		{
			name:   "amneziawg obfuscation low header",
			ok:     false,
			schema: "AmneziaWGObfuscation",
			value:  `{"h1":4,"h2":6,"h3":7,"h4":8,"jc":1,"jmax":128,"jmin":64,"s1":1,"s2":2,"s3":3,"s4":4}`,
		},
		{
			name:   "api info",
			ok:     true,
			schema: "APIInfo",
			value:  `{"tls_sha256":"` + fingerprint + `","urls":["https://x"]}`,
		},
		{
			name:   "api info http url",
			ok:     false,
			schema: "APIInfo",
			value:  `{"tls_sha256":"` + fingerprint + `","urls":["http://x"]}`,
		},
		{
			name:   "api info no urls",
			ok:     false,
			schema: "APIInfo",
			value:  `{"tls_sha256":"` + fingerprint + `","urls":[]}`,
		},
		{
			name:   "api info short fingerprint",
			ok:     false,
			schema: "APIInfo",
			value:  `{"tls_sha256":"00","urls":["https://x"]}`,
		},
		{
			name:   "credential",
			ok:     true,
			schema: "Credential",
			value:  `{"ticket":"` + strings.Repeat("A", 512) + `","type":"ticket"}`,
		},
		{
			name:   "credential long ticket",
			ok:     false,
			schema: "Credential",
			value:  `{"ticket":"` + strings.Repeat("A", 513) + `","type":"ticket"}`,
		},
		{
			name:   "hysteria2 metadata short pin",
			ok:     false,
			schema: "Hysteria2Metadata",
			value:  `{"obfs_password":"","port":1,"tls_pin":"AA:BB"}`,
		},
		{
			name:   "inbound info",
			ok:     true,
			schema: "InboundInfo",
			value:  `{"port":443,"protocol":"vless","security":"none","transport":"tcp"}`,
		},
		{
			name:   "inbound info unknown transport",
			ok:     false,
			schema: "InboundInfo",
			value:  `{"port":443,"protocol":"vless","security":"none","transport":"quic"}`,
		},
		{
			name:   "location",
			ok:     true,
			schema: "Location",
			value:  `{"country":"DE"}`,
		},
		{
			name:   "location lowercase country",
			ok:     false,
			schema: "Location",
			value:  `{"country":"de"}`,
		},
		{
			name:   "open session",
			ok:     true,
			schema: "OpenSessionResponse",
			value:  fmt.Sprintf(openSession, uuid.NewString()),
		},
		{
			name:   "open session bad id",
			ok:     false,
			schema: "OpenSessionResponse",
			value:  fmt.Sprintf(openSession, "abc"),
		},
		{
			name:   "openvpn metadata unknown protocol",
			ok:     false,
			schema: "OpenVPNMetadata",
			value:  `{"ca":"","port":1,"protocol":"sctp","tls_crypt":""}`,
		},
		{
			name:   "public key",
			ok:     true,
			schema: "PublicKey",
			value:  `"` + solana.SystemProgramID.String() + `"`,
		},
		{
			name:   "public key malformed",
			ok:     false,
			schema: "PublicKey",
			value:  `"not-a-key"`,
		},
		{
			name:   "settlement",
			ok:     true,
			schema: "Settlement",
			value:  fmt.Sprintf(settlement, 5),
		},
		{
			name:   "settlement heartbeat over",
			ok:     false,
			schema: "Settlement",
			value:  fmt.Sprintf(settlement, 61),
		},
		{
			name:   "settlement heartbeat zero",
			ok:     false,
			schema: "Settlement",
			value:  fmt.Sprintf(settlement, 0),
		},
		{
			name:   "terms",
			ok:     true,
			schema: "Terms",
			value:  `{"basis":"per_gb","price":1}`,
		},
		{
			name:   "terms price over int64",
			ok:     false,
			schema: "Terms",
			value:  `{"basis":"per_gb","price":10000000000000000000}`,
		},
		{
			name:   "voucher",
			ok:     true,
			schema: "Voucher",
			value:  `{"cumulative":1,"signature":"` + strings.Repeat("A", 88) + `"}`,
		},
		{
			name:   "voucher short signature",
			ok:     false,
			schema: "Voucher",
			value:  `{"cumulative":1,"signature":"` + strings.Repeat("A", 87) + `"}`,
		},
		{
			name:   "wireguard peer response",
			ok:     true,
			schema: "WireGuardPeerResponse",
			value:  `{"addresses":["10.0.0.2/32"],"mtu":1280,"port":1,"public_key":"` + key + `"}`,
		},
		{
			name:   "wireguard peer response low mtu",
			ok:     false,
			schema: "WireGuardPeerResponse",
			value:  `{"addresses":["10.0.0.2/32"],"mtu":1279,"port":1,"public_key":"` + key + `"}`,
		},
	}

	spec, err := nodeapi.GetSpec()
	require.NoError(t, err)

	schemas := spec.Components.Schemas
	require.NotContains(t, schemas["OpenSessionResponse"].Value.Properties, "addresses")

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var value any
			require.NoError(t, json.Unmarshal([]byte(tc.value), &value))

			err := schemas[tc.schema].Value.VisitJSON(value)
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestGetSpec_DefaultErrorResponses(t *testing.T) {
	t.Parallel()

	spec, err := nodeapi.GetSpec()
	require.NoError(t, err)

	for path, item := range spec.Paths.Map() {
		for method, operation := range item.Operations() {
			require.NotNil(t, operation.Responses.Default(), "%s %s", method, path)
		}
	}
}

func TestGetSpec_ErrorCodes(t *testing.T) {
	t.Parallel()

	spec, err := nodeapi.GetSpec()
	require.NoError(t, err)

	enum := spec.Components.Schemas["ErrorCode"].Value.Enum

	codes := make([]string, 0, len(enum))
	for _, value := range enum {
		codes = append(codes, value.(string))
	}

	require.ElementsMatch(t, api.Codes(), codes)
}

func TestGetSpec_ErrorMessageBound(t *testing.T) {
	t.Parallel()

	spec, err := nodeapi.GetSpec()
	require.NoError(t, err)

	errorSchema := spec.Components.Schemas["ErrorResponse"].Value.Properties["error"].Value
	message := errorSchema.Properties["message"].Value
	require.NotNil(t, message.MaxLength)
	require.EqualValues(t, api.MaxErrorMessageBytes, *message.MaxLength)
	require.Equal(t, `^[\x20-\x7e]*$`, message.Pattern)
}

func TestGetSpec_NoServers(t *testing.T) {
	t.Parallel()

	spec, err := nodeapi.GetSpec()
	require.NoError(t, err)
	require.Empty(t, spec.Servers)
}

func TestGetSpec_SchemaProperties(t *testing.T) {
	t.Parallel()

	spec, err := nodeapi.GetSpec()
	require.NoError(t, err)

	types := map[string]any{
		"Consumption":     nodeapi.Consumption{},
		"ErrorResponse":   api.ErrorResponse{},
		"Ledger":          nodeapi.Ledger{},
		"PeerRequest":     service.PeerRequest{},
		"PeerResponse":    service.PeerResponse{},
		"ServiceMetadata": service.ServiceMetadata{},
		"SignedMessage":   auth.SignedMessage{},
		"Terms":           session.Terms{},
		"Usage":           session.Usage{},
	}

	for name, value := range types {
		schema := spec.Components.Schemas[name].Value
		require.NotNil(t, schema, name)
		require.ElementsMatch(t, jsonFieldNames(value), propertyNames(schema.Properties), name)
	}
}

func TestNewInsecureClient_RejectsInvalidResponse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		body string
		name string
	}{
		{
			body: `{"status":"nope","version":"0"}`,
			name: "off schema",
		},
		{
			body: `{"status":"ok","version":"0","VERSION":"\u001b[31mpwned"}`,
			name: "case variant key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}

			srv := httptest.NewServer(http.HandlerFunc(handler))
			t.Cleanup(srv.Close)

			client, err := nodeapi.NewInsecureClient(srv.URL, time.Second)
			require.NoError(t, err)

			_, err = client.GetHealthWithResponse(t.Context())
			require.ErrorIs(t, err, api.ErrInvalidResponse)
		})
	}
}

func TestNewInsecureClient_Retry(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32

	handler := func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"code":"rate_limited","message":"slow down"}}`)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok","version":"0.0.0"}`)
	}

	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)

	client, err := nodeapi.NewInsecureClient(srv.URL, time.Second)
	require.NoError(t, err)

	resp, err := client.GetHealthWithResponse(t.Context())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode())
	require.Equal(t, nodeapi.HealthStatusOK, resp.JSON200.Status)
	require.EqualValues(t, 2, hits.Load())
}

//nolint:paralleltest
func TestNewPinnedClient_FreshSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)

	key, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	var (
		mu   sync.Mutex
		seen []signedRequest
	)

	handler := func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			api.Abort(c, err)

			return
		}

		signer, _ := api.SignerFrom(c)
		req := signedRequest{
			body:   body,
			signer: signer,
		}

		mu.Lock()

		seen = append(seen, req)
		first := len(seen) == 1

		mu.Unlock()

		if first {
			c.Header("Retry-After", "0")
			api.Abort(c, api.ErrRateLimited)

			return
		}

		position := nodeapi.Position{
			Ledger: nodeapi.Ledger{
				Basis:  session.BasisPerGB,
				Status: nodeapi.LedgerStatusOpen,
			},
		}
		c.JSON(http.StatusOK, position)
	}

	options := api.EngineOptions{Logger: zerolog.Nop()}
	engine := api.NewEngine(options)
	engine.POST("/v1/channels/:channel/vouchers", api.RequireSignature(auth.NewNonceCache(time.Minute)), handler)

	srv := httptest.NewTLSServer(engine)
	t.Cleanup(srv.Close)

	client, err := nodeapi.NewPinnedClient(srv.URL, api.Fingerprint(srv.Certificate()), key, time.Second)
	require.NoError(t, err)

	body := nodeapi.SendVoucherRequest{
		Voucher: nodeapi.Voucher{Cumulative: 1, Signature: make([]byte, solana.SignatureLength)},
	}

	resp, err := client.SendChannelVoucherWithResponse(t.Context(), key.PublicKey(), body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode())

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, seen, 2)
	require.NotEmpty(t, seen[0].body)
	require.Equal(t, seen[0].body, seen[1].body)
	require.Equal(t, key.PublicKey(), seen[0].signer)
	require.Equal(t, key.PublicKey(), seen[1].signer)
}

func TestNewPinnedClient_RejectsOversizedResponse(t *testing.T) {
	t.Parallel()

	key, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok","version":"0.0.0"}`)
		_, _ = io.WriteString(w, strings.Repeat(" ", api.MaxResponseBodyBytes))
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)

	client, err := nodeapi.NewPinnedClient(srv.URL, api.Fingerprint(srv.Certificate()), key, time.Second)
	require.NoError(t, err)

	_, err = client.GetHealthWithResponse(t.Context())
	require.ErrorIs(t, err, api.ErrResponseTooLarge)
}

func TestOpenSession200JSONResponse_VisitOpenSessionResponse_Ticket(t *testing.T) {
	t.Parallel()

	consumption := nodeapi.Consumption{Usage: session.Usage{RxBytes: 1}}
	tests := map[string]func(http.ResponseWriter) error{
		"open": nodeapi.OpenSession200JSONResponse{
			Consumption: consumption,
			Peer:        service.PeerResponse{Name: wireguard.Name},
			SessionID:   "ticket",
		}.VisitOpenSessionResponse,
		"poll": nodeapi.GetSession200JSONResponse{
			Consumption: consumption,
			Status:      "active",
		}.VisitGetSessionResponse,
	}

	for name, visit := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			response := httptest.NewRecorder()
			require.NoError(t, visit(response))
			require.Equal(t, http.StatusOK, response.Code)

			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &fields))
			require.Contains(t, fields, "consumption")
			require.NotContains(t, fields, "ledger")
		})
	}
}

//nolint:paralleltest
func TestRegisterRoutes_SignedAndUnsigned(t *testing.T) {
	gin.SetMode(gin.TestMode)

	key, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	svc := &fakeService{}
	srv := startRoutes(t, svc)
	sessionID := uuid.NewString()

	client, err := nodeapi.NewInsecureClient(srv.URL, time.Second)
	require.NoError(t, err)

	health, err := client.GetHealthWithResponse(t.Context())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, health.StatusCode())
	require.Equal(t, nodeapi.HealthStatusOK, health.JSON200.Status)

	unsigned, err := client.GetSessionWithResponse(t.Context(), sessionID)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, unsigned.StatusCode())

	transport := &api.SignedTransport{Signer: key}
	signed := &http.Client{Transport: transport}
	signedClient, err := nodeapi.NewClientWithResponses(srv.URL, nodeapi.WithHTTPClient(signed))
	require.NoError(t, err)

	got, err := signedClient.GetSessionWithResponse(t.Context(), sessionID)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, got.StatusCode())
	require.EqualValues(t, 1, got.JSON200.Consumption.Usage.RxBytes)
	require.Equal(t, key.PublicKey(), svc.signer)

	missing, err := signedClient.GetSessionWithResponse(t.Context(), missingSessionID)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, missing.StatusCode())
	require.Equal(t, api.CodeSessionNotFound, missing.JSONDefault.Error.Code)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/v1/channels/not-a-key", nil)
	require.NoError(t, err)
	resp, err := signed.Do(req)
	require.NoError(t, err)

	defer func() { require.NoError(t, resp.Body.Close()) }()

	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Contains(t, body, "error")

	errBody, ok := body["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, api.CodeBadRequest, errBody["code"])
	require.NotContains(t, body, "msg")
}

//nolint:paralleltest
func TestRegisterRoutes_ValidatesRequests(t *testing.T) {
	key, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	svc := &fakeService{}
	srv := startRoutes(t, svc)
	signed := &http.Client{Transport: &api.SignedTransport{Signer: key}}
	cases := []struct {
		body   string
		method string
		name   string
		path   string
	}{
		{
			body: `{"credential":{"type":"channel","channel":"11111111111111111111111111111111"},` +
				`"peer":{"type":"wireguard","payload":{}},"CREDENTIAL":{"type":"ticket"}}`,
			method: http.MethodPost,
			name:   "case variant open body key",
			path:   "/v1/sessions",
		},
		{
			body:   `{"peer":{}}`,
			method: http.MethodPost,
			name:   "invalid open body",
			path:   "/v1/sessions",
		},
		{
			method: http.MethodGet,
			name:   "invalid session id",
			path:   "/v1/sessions/abc",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), tc.method, srv.URL+tc.path, strings.NewReader(tc.body))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")

			resp, err := signed.Do(req)
			require.NoError(t, err)

			defer func() { require.NoError(t, resp.Body.Close()) }()

			var body api.ErrorResponse
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			require.Equal(t, api.CodeBadRequest, body.Error.Code)
		})
	}

	require.Zero(t, svc.opened.Load())
}

func TestStreamingPaths_MatchSpec(t *testing.T) {
	t.Parallel()

	spec, err := nodeapi.GetSpec()
	require.NoError(t, err)

	paths := nodeapi.StreamingPaths()
	require.ElementsMatch(t, []string{"/v1/leases/:lease/events", "/v1/sessions/:id/events"}, paths)

	for _, path := range paths {
		specPath := strings.NewReplacer(":id", "{id}", ":lease", "{lease}").Replace(path)
		item := spec.Paths.Find(specPath)
		require.NotNil(t, item, specPath)
		require.NotNil(t, item.Get, specPath)
		require.Contains(t, item.Get.Responses.Status(200).Value.Content, "text/event-stream")
	}
}

func TestVerifyMetadata(t *testing.T) {
	t.Parallel()

	operatorKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	owner := solana.PublicKey{1}
	node := solana.PublicKey{2}
	programID := solana.MustPublicKeyFromBase58("SentineLDVPN1111111111111111111111111111111")
	now := time.Unix(0, 0)

	metadata := metadataFixture(node, owner, operatorKey.PublicKey(), programID, now)

	signed, err := nodeapi.SignMetadata(operatorKey, metadata)
	require.NoError(t, err)

	src := fakeGrantSource{operatorKey.PublicKey(): true}

	got, err := nodeapi.VerifyMetadata(t.Context(), src, signed, node, owner, programID, now.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, metadata.API, got.API)

	_, err = nodeapi.VerifyMetadata(t.Context(), src, signed, node, owner, programID, now.Add(2*time.Hour))
	require.ErrorIs(t, err, auth.ErrMetadataExpired)

	_, err = nodeapi.VerifyMetadata(t.Context(), src, signed, owner, owner, programID, now)
	require.ErrorIs(t, err, auth.ErrMetadataMismatch)

	_, err = nodeapi.VerifyMetadata(t.Context(), fakeGrantSource{}, signed, node, owner, programID, now)
	require.Error(t, err)
}

func TestVerifyMetadata_FutureAndOverlong(t *testing.T) {
	t.Parallel()

	operatorKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	owner := solana.PublicKey{1}
	node := solana.PublicKey{2}
	programID := solana.MustPublicKeyFromBase58("SentineLDVPN1111111111111111111111111111111")
	now := time.Unix(0, 0)
	src := fakeGrantSource{operatorKey.PublicKey(): true}

	future := metadataFixture(node, owner, operatorKey.PublicKey(), programID, now)
	future.IssuedAt = now.Add(2 * auth.MaxRequestSkew).Unix()

	futureSigned, err := nodeapi.SignMetadata(operatorKey, future)
	require.NoError(t, err)

	_, err = nodeapi.VerifyMetadata(t.Context(), src, futureSigned, node, owner, programID, now)
	require.ErrorIs(t, err, auth.ErrMetadataNotYetValid)

	overlong := metadataFixture(node, owner, operatorKey.PublicKey(), programID, now)
	overlong.ExpiresAt = now.Add(2 * auth.MetadataTTL).Unix()

	overlongSigned, err := nodeapi.SignMetadata(operatorKey, overlong)
	require.NoError(t, err)

	_, err = nodeapi.VerifyMetadata(t.Context(), src, overlongSigned, node, owner, programID, now)
	require.ErrorIs(t, err, auth.ErrMetadataWindowTooLong)
}
