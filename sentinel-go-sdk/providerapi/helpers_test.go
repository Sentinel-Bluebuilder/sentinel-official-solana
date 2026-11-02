package providerapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/providerapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

type countingService struct {
	providerapi.Unimplemented

	issued atomic.Int32
}

func (c *countingService) IssueTicket(
	context.Context,
	providerapi.IssueTicketRequestObject,
) (providerapi.IssueTicketResponseObject, error) {
	c.issued.Add(1)

	return nil, api.ErrInternal
}

type fakeGrantSource map[solana.PublicKey]bool

func (m fakeGrantSource) HasGrant(
	_ context.Context,
	_, signer solana.PublicKey,
	_ uint64,
	_ ...solana.PublicKey,
) (bool, error) {
	return m[signer], nil
}

type fakeRateLimitingServer struct {
	bodies [][]byte
	mu     sync.Mutex
}

func (l *fakeRateLimitingServer) ServeHTTP(writer http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)

		return
	}

	l.mu.Lock()

	l.bodies = append(l.bodies, body)
	first := len(l.bodies) == 1

	l.mu.Unlock()

	writer.Header().Set("Content-Type", "application/json")

	if first {
		writer.Header().Set("Retry-After", "0")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(writer, `{"error":{"code":"rate_limited","message":"slow down"}}`)

		return
	}

	_, _ = io.WriteString(writer, `{"data_cap_bytes":1,"expires_at":2,"ticket":"t"}`)
}

func (l *fakeRateLimitingServer) received() [][]byte {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Clone(l.bodies)
}

type fakeService struct {
	providerapi.Unimplemented
}

func (fakeService) GetNodes(
	context.Context,
	providerapi.GetNodesRequestObject,
) (providerapi.GetNodesResponseObject, error) {
	return providerapi.GetNodes200JSONResponse{Nodes: []providerapi.RosterNode{}}, nil
}

func (fakeService) IssueTicket(
	_ context.Context,
	req providerapi.IssueTicketRequestObject,
) (providerapi.IssueTicketResponseObject, error) {
	if req.Body.TicketRequest == "bad" {
		return nil, api.ErrInvalidTicket
	}

	resp := providerapi.IssueTicket200JSONResponse{
		DataCapBytes: 1,
		ExpiresAt:    2,
		Ticket:       "t",
	}

	return resp, nil
}

func jsonFieldNames(value any) []string {
	valueType := reflect.TypeOf(value)

	names := make([]string, 0, valueType.NumField())
	for field := range valueType.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}

		names = append(names, name)
	}

	return names
}

func jsonObject(t *testing.T, value any) map[string]any {
	t.Helper()

	data, err := json.Marshal(value)
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(data, &out))

	return out
}

func metadataFixture(provider, owner, operator, programID solana.PublicKey, now time.Time) providerapi.Metadata {
	return providerapi.Metadata{
		API:        providerapi.APIInfo{URLs: []string{"https://127.0.0.1:8443"}, TLSSHA256: "00"},
		ExpiresAt:  now.Add(auth.MetadataTTL).Unix(),
		IssuedAt:   now.Unix(),
		Nodes:      []providerapi.PublicKey{},
		Operator:   operator,
		Owner:      owner,
		Plans:      []providerapi.PublicKey{},
		ProgramID:  programID,
		Provider:   provider,
		SDKVersion: "0.0.0",
		Version:    1,
	}
}

func propertyNames(props openapi3.Schemas) []string {
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}

	return names
}

func requireTicketAfterRateLimit(
	t *testing.T,
	client *providerapi.ClientWithResponses,
	server *fakeRateLimitingServer,
) {
	t.Helper()

	body := providerapi.IssueTicketJSONRequestBody{TicketRequest: "good"}

	resp, err := client.IssueTicketWithResponse(t.Context(), body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode())
	require.Equal(t, "t", resp.JSON200.Ticket)

	bodies := server.received()
	require.Len(t, bodies, 2)
	require.NotEmpty(t, bodies[0])
	require.Equal(t, bodies[0], bodies[1])
}

func seedGrant(t *testing.T, fakeRPC *sdktest.FakeRPC, owner, operator solana.PublicKey) {
	t.Helper()

	err := fakeRPC.SetGrant(dvpn.ProgramID, owner, operator, solana.PublicKey{}, program.PermSignProviderMetadata)
	require.NoError(t, err)
}

func startSignedMetadataServer(t *testing.T, signed auth.SignedMessage) *httptest.Server {
	t.Helper()

	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(signed) //nolint:errchkjson
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)

	return srv
}
