package nodeapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
	"github.com/sentinel-official/sentinel-go-sdk/v3/wireguard"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const missingSessionID = "00000000-0000-4000-8000-000000000000"

type fakeGrantSource map[solana.PublicKey]bool

func (m fakeGrantSource) HasGrant(
	_ context.Context,
	_, signer solana.PublicKey,
	_ uint64,
	_ ...solana.PublicKey,
) (bool, error) {
	return m[signer], nil
}

type fakeService struct {
	nodeapi.Unimplemented

	opened atomic.Int32
	signer solana.PublicKey
}

func (f *fakeService) GetHealth(
	context.Context,
	nodeapi.GetHealthRequestObject,
) (nodeapi.GetHealthResponseObject, error) {
	resp := nodeapi.GetHealth200JSONResponse{
		Status:  "ok",
		Version: "0.0.0",
	}

	return resp, nil
}

func (f *fakeService) GetSession(
	ctx context.Context,
	req nodeapi.GetSessionRequestObject,
) (nodeapi.GetSessionResponseObject, error) {
	signer, ok := api.SignerFrom(ctx)
	if !ok {
		return nil, api.ErrUnauthorized
	}

	f.signer = signer

	if req.ID == missingSessionID {
		return nil, api.ErrSessionNotFound
	}

	consumption := nodeapi.Consumption{Usage: session.Usage{RxBytes: 1}}
	ledger := nodeapi.Ledger{Basis: session.BasisPerGB}
	resp := nodeapi.GetSession200JSONResponse{
		Consumption: consumption,
		Ledger:      ledger,
		Status:      "active",
	}

	return resp, nil
}

func (f *fakeService) OpenSession(
	context.Context,
	nodeapi.OpenSessionRequestObject,
) (nodeapi.OpenSessionResponseObject, error) {
	f.opened.Add(1)

	return nil, api.ErrInternal
}

type signedRequest struct {
	body   []byte
	signer solana.PublicKey
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

func metadataFixture(node, owner, operator, programID solana.PublicKey, now time.Time) nodeapi.Metadata {
	return nodeapi.Metadata{
		API:        nodeapi.APIInfo{URLs: []string{"https://127.0.0.1:8443"}, TLSSHA256: strings.Repeat("0", 64)},
		ExpiresAt:  now.Add(auth.MetadataTTL).Unix(),
		IssuedAt:   now.Unix(),
		Location:   nodeapi.Location{Country: "DE"},
		Node:       node,
		Operator:   operator,
		Owner:      owner,
		ProgramID:  programID,
		SDKVersion: "0.0.0",
		Service:    nodeapi.ServiceMetadata{Name: wireguard.Name, Payload: json.RawMessage(`{}`)},
		Settlement: nodeapi.Settlement{
			HeartbeatSeconds:        5,
			MinChannelPrepayBytes:   1,
			MinChannelPrepaySeconds: 1,
			MinLeasePrepaySeconds:   1,
		},
		Version: 1,
	}
}

func propertyNames(props openapi3.Schemas) []string {
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}

	return names
}

func seedGrant(t *testing.T, fakeRPC *sdktest.FakeRPC, owner, operator solana.PublicKey) {
	t.Helper()
	require.NoError(t, fakeRPC.SetGrant(dvpn.ProgramID, owner, operator, solana.PublicKey{}, program.PermSignNodeMetadata))
}

func startRoutes(t *testing.T, svc *fakeService) *httptest.Server {
	t.Helper()

	gin.SetMode(gin.TestMode)

	options := api.EngineOptions{Logger: zerolog.Nop()}
	engine := api.NewEngine(options)
	nodeapi.RegisterRoutes(
		engine,
		svc,
		auth.NewNonceCache(time.Minute),
		api.NewLimiter(100, 100),
		api.NewLimiter(100, 100),
	)

	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)

	return srv
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
