package providerapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/providerapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

type shoutedMetadata struct {
	providerapi.Metadata

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
	providerAddress := solana.PublicKey{2}

	nodes := make([]providerapi.PublicKey, 4097)
	for i := range nodes {
		nodes[i] = providerAddress
	}

	cases := []struct {
		err     error
		mutate  func(*providerapi.Metadata)
		name    string
		payload func(providerapi.Metadata) any
		uri     func(string) string
		uriLen  uint8
	}{
		{
			err:  api.ErrInvalidResponse,
			name: "case variant payload key",
			payload: func(m providerapi.Metadata) any {
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
			mutate: func(m *providerapi.Metadata) { m.API.URLs = []string{"https://x:0"} },
			name:   "invalid api url",
		},
		{
			err:    api.ErrInvalidResponse,
			mutate: func(m *providerapi.Metadata) { m.Nodes = nodes },
			name:   "too many nodes",
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

			metadata := metadataFixture(
				providerAddress,
				ownerKey.PublicKey(),
				operatorKey.PublicKey(),
				dvpn.ProgramID,
				time.Now(),
			)
			metadata.API.TLSSHA256 = strings.Repeat("0", 64)

			if tc.mutate != nil {
				tc.mutate(&metadata)
			}

			signed, err := providerapi.SignMetadata(operatorKey, metadata)
			if tc.payload != nil {
				signed, err = auth.SignMessage(operatorKey, auth.DomainProviderMetadata, tc.payload(metadata))
			}

			require.NoError(t, err)

			srv := startSignedMetadataServer(t, signed)

			uri := srv.URL
			if tc.uri != nil {
				uri = tc.uri(srv.URL)
			}

			provider := dvpn.Provider{Owner: ownerKey.PublicKey(), URILen: uint8(len(uri))}
			copy(provider.URI[:], uri)

			if tc.uriLen != 0 {
				provider.URILen = tc.uriLen
			}

			got, url, err := providerapi.Discover(t.Context(), programClient, providerAddress, provider, time.Second)
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

func TestGetSpec_Constraints(t *testing.T) {
	t.Parallel()

	spec, err := providerapi.GetSpec()
	require.NoError(t, err)

	key := solana.PublicKey{1}

	nodes := make([]any, 4097)
	for i := range nodes {
		nodes[i] = key.String()
	}

	metadata := metadataFixture(key, key, key, key, time.Unix(0, 0))
	metadata.API.TLSSHA256 = strings.Repeat("0", 64)
	ticket := providerapi.IssueTicketResponse{DataCapBytes: 1, ExpiresAt: 2, Ticket: "dA=="}
	roster := providerapi.RosterNode{Live: true, Node: key, ProbedAt: 1}

	cases := []struct {
		mutate func(map[string]any)
		name   string
		schema string
		value  any
	}{
		{
			mutate: func(m map[string]any) { m["nodes"] = nodes },
			name:   "metadata nodes",
			schema: "Metadata",
			value:  metadata,
		},
		{
			mutate: func(m map[string]any) { m["issued_at"] = float64(-1) },
			name:   "negative metadata issued_at",
			schema: "Metadata",
			value:  metadata,
		},
		{
			mutate: func(m map[string]any) { m["ticket"] = strings.Repeat("A", 513) },
			name:   "ticket length",
			schema: "IssueTicketResponse",
			value:  ticket,
		},
		{
			mutate: func(m map[string]any) { m["probed_at"] = float64(-1) },
			name:   "negative probed_at",
			schema: "RosterNode",
			value:  roster,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			schema := spec.Components.Schemas[tc.schema].Value
			value := jsonObject(t, tc.value)
			require.NoError(t, schema.VisitJSON(value))

			tc.mutate(value)
			require.Error(t, schema.VisitJSON(value))
		})
	}
}

func TestGetSpec_DefaultErrorResponses(t *testing.T) {
	t.Parallel()

	spec, err := providerapi.GetSpec()
	require.NoError(t, err)

	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			require.NotNil(t, op.Responses.Default(), "%s %s", method, path)
		}
	}
}

func TestGetSpec_ErrorCodes(t *testing.T) {
	t.Parallel()

	spec, err := providerapi.GetSpec()
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

	spec, err := providerapi.GetSpec()
	require.NoError(t, err)

	message := spec.Components.Schemas["ErrorResponse"].Value.Properties["error"].Value.Properties["message"].Value
	require.NotNil(t, message.MaxLength)
	require.EqualValues(t, api.MaxErrorMessageBytes, *message.MaxLength)
	require.Equal(t, `^[\x20-\x7e]*$`, message.Pattern)
}

func TestGetSpec_NoServers(t *testing.T) {
	t.Parallel()

	spec, err := providerapi.GetSpec()
	require.NoError(t, err)
	require.Empty(t, spec.Servers)
}

func TestGetSpec_SchemaProperties(t *testing.T) {
	t.Parallel()

	spec, err := providerapi.GetSpec()
	require.NoError(t, err)

	types := map[string]any{
		"ErrorResponse": api.ErrorResponse{},
		"SignedMessage": auth.SignedMessage{},
	}

	for name, value := range types {
		schema := spec.Components.Schemas[name].Value
		require.NotNil(t, schema, name)
		require.ElementsMatch(t, jsonFieldNames(value), propertyNames(schema.Properties), name)
	}
}

func TestNewPinnedClient_RejectsInvalidResponse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		body string
		name string
	}{
		{
			body: `{"nodes":[{"live":true,"node":"11111111111111111111111111111112","probed_at":-1}]}`,
			name: "off schema",
		},
		{
			body: `{"nodes":[{"live":true,"node":"11111111111111111111111111111112","probed_at":1,"PROBED_AT":-1}]}`,
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

			srv := httptest.NewTLSServer(http.HandlerFunc(handler))
			t.Cleanup(srv.Close)

			client, err := providerapi.NewPinnedClient(srv.URL, api.Fingerprint(srv.Certificate()), time.Second)
			require.NoError(t, err)

			_, err = client.GetNodesWithResponse(t.Context())
			require.ErrorIs(t, err, api.ErrInvalidResponse)
		})
	}
}

func TestNewPinnedClient_Retry(t *testing.T) {
	t.Parallel()

	server := &fakeRateLimitingServer{}

	srv := httptest.NewTLSServer(server)
	t.Cleanup(srv.Close)

	client, err := providerapi.NewPinnedClient(srv.URL, api.Fingerprint(srv.Certificate()), time.Second)
	require.NoError(t, err)

	requireTicketAfterRateLimit(t, client, server)
}

func TestRegisterRoutes(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	opts := api.EngineOptions{Logger: zerolog.Nop()}
	engine := api.NewEngine(opts)
	providerapi.RegisterRoutes(engine, fakeService{}, api.NewLimiter(100, 100))

	srv := httptest.NewServer(engine)
	defer srv.Close()

	client, err := providerapi.NewInsecureClient(srv.URL, time.Second)
	require.NoError(t, err)

	nodes, err := client.GetNodesWithResponse(t.Context())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, nodes.StatusCode())

	body := providerapi.IssueTicketJSONRequestBody{TicketRequest: "good"}
	ok, err := client.IssueTicketWithResponse(t.Context(), body)
	require.NoError(t, err)
	require.Equal(t, "t", ok.JSON200.Ticket)

	body.TicketRequest = "bad"
	bad, err := client.IssueTicketWithResponse(t.Context(), body)
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, bad.StatusCode())
}

func TestRegisterRoutes_ValidatesRequests(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	svc := &countingService{}
	engine := api.NewEngine(api.EngineOptions{Logger: zerolog.Nop()})
	providerapi.RegisterRoutes(engine, svc, api.NewLimiter(100, 100))

	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { require.Zero(t, svc.issued.Load()) })

	cases := []struct {
		body string
		name string
	}{
		{
			body: `{"ticket_request":1}`,
			name: "off schema",
		},
		{
			body: `{"ticket_request":"dA==","TICKET_REQUEST":"!!"}`,
			name: "case variant key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			url := srv.URL + "/v1/tickets"
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(tc.body))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")

			resp, err := srv.Client().Do(req)
			require.NoError(t, err)

			defer func() { require.NoError(t, resp.Body.Close()) }()

			var body api.ErrorResponse
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			require.Equal(t, api.CodeBadRequest, body.Error.Code)
		})
	}
}

func TestVerifyMetadata(t *testing.T) {
	t.Parallel()

	operatorKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	owner := solana.PublicKey{1}
	provider := solana.PublicKey{2}
	programID := solana.MustPublicKeyFromBase58("SentineLDVPN1111111111111111111111111111111")
	now := time.Unix(0, 0)

	metadata := metadataFixture(provider, owner, operatorKey.PublicKey(), programID, now)

	signed, err := providerapi.SignMetadata(operatorKey, metadata)
	require.NoError(t, err)

	src := fakeGrantSource{operatorKey.PublicKey(): true}

	got, err := providerapi.VerifyMetadata(t.Context(), src, signed, provider, owner, programID, now.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, metadata.API, got.API)

	_, err = providerapi.VerifyMetadata(t.Context(), src, signed, provider, owner, programID, now.Add(2*time.Hour))
	require.ErrorIs(t, err, auth.ErrMetadataExpired)

	_, err = providerapi.VerifyMetadata(t.Context(), src, signed, owner, owner, programID, now)
	require.ErrorIs(t, err, auth.ErrMetadataMismatch)

	_, err = providerapi.VerifyMetadata(t.Context(), fakeGrantSource{}, signed, provider, owner, programID, now)
	require.Error(t, err)
}

func TestVerifyMetadata_FutureAndOverlong(t *testing.T) {
	t.Parallel()

	operatorKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	owner := solana.PublicKey{1}
	provider := solana.PublicKey{2}
	programID := solana.MustPublicKeyFromBase58("SentineLDVPN1111111111111111111111111111111")
	now := time.Unix(0, 0)
	src := fakeGrantSource{operatorKey.PublicKey(): true}

	future := metadataFixture(provider, owner, operatorKey.PublicKey(), programID, now)
	future.IssuedAt = now.Add(2 * auth.MaxRequestSkew).Unix()

	futureSigned, err := providerapi.SignMetadata(operatorKey, future)
	require.NoError(t, err)

	_, err = providerapi.VerifyMetadata(t.Context(), src, futureSigned, provider, owner, programID, now)
	require.ErrorIs(t, err, auth.ErrMetadataNotYetValid)

	overlong := metadataFixture(provider, owner, operatorKey.PublicKey(), programID, now)
	overlong.ExpiresAt = now.Add(2 * auth.MetadataTTL).Unix()

	overlongSigned, err := providerapi.SignMetadata(operatorKey, overlong)
	require.NoError(t, err)

	_, err = providerapi.VerifyMetadata(t.Context(), src, overlongSigned, provider, owner, programID, now)
	require.ErrorIs(t, err, auth.ErrMetadataWindowTooLong)
}
