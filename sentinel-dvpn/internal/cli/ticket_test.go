package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpn/internal/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	sdkcli "github.com/sentinel-official/sentinel-go-sdk/v3/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/providerapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

type recordingResponseWriter struct {
	http.ResponseWriter

	encoded chan<- string
}

func (w recordingResponseWriter) Write(data []byte) (int, error) {
	var response providerapi.IssueTicket200JSONResponse
	if json.Unmarshal(data, &response) == nil && response.Ticket != "" {
		body, err := json.Marshal(response)
		if err != nil {
			return 0, fmt.Errorf("encoding provider response: %w", err)
		}

		w.encoded <- response.Ticket

		if _, err := w.ResponseWriter.Write(body); err != nil {
			return 0, fmt.Errorf("writing provider response: %w", err)
		}

		return len(data), nil
	}

	n, err := w.ResponseWriter.Write(data)
	if err != nil {
		return n, fmt.Errorf("writing provider response: %w", err)
	}

	return n, nil
}

func TestNewTicketVerifyCmd_RejectsWrongNode(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fixture := newRunnerFixture(t, fakeRPC)
	chainCfg := dvpn.Config{Mint: testMint}
	seedConfigAccount(t, fakeRPC, chainCfg)

	ownerKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	providerKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	providerAddress := providerKey.PublicKey()

	nodeKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	otherNodeKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	provider := dvpn.Provider{Owner: ownerKey.PublicKey()}
	seedProviderAccount(t, fakeRPC, providerAddress, provider)

	ticket := auth.Ticket{
		ExpiresUnix: time.Now().Add(time.Hour).Unix(),
		Node:        nodeKey.PublicKey(),
		Provider:    providerAddress,
		Version:     auth.TicketVersion,
	}
	signedTicket := auth.SignTicket(ownerKey, ticket)

	cmd := cli.NewTicketVerifyCmd(fixture.programClient, fixture.printer.Printer)
	args := []string{signedTicket.Encode(), "--node", otherNodeKey.PublicKey().String()}
	cmd.SetArgs(args)
	cmd.SetContext(t.Context())

	require.ErrorIs(t, cmd.Execute(), auth.ErrTicketNodeMismatch)
}

//nolint:paralleltest
func TestRequestTicket_RefusesPrivateProviderWithoutOptIn(t *testing.T) {
	fakeRPC := sdktest.NewFakeRPC(t)
	fixture := newRunnerFixture(t, fakeRPC)
	seedConfigAccount(t, fakeRPC, dvpn.Config{Mint: testMint})

	providerKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	nodeKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	subscriptionKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	var requests atomic.Int32

	srv, _ := startProviderAPI(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
			requests.Add(1)
			next.ServeHTTP(writer, req)
		})
	})

	uri, uriLen := packURI(srv.URL)
	seedProviderAccount(t, fakeRPC, providerKey.PublicKey(), dvpn.Provider{URI: uri, URILen: uriLen})

	cmd := cli.NewTicketRequestCmd(
		fixture.programClient,
		fixture.signer,
		sdkcli.PeerConfig{},
		fixture.printer.Printer,
	)
	cmd.SetArgs([]string{
		"--provider", providerKey.PublicKey().String(),
		"--node", nodeKey.PublicKey().String(),
		"--subscription", subscriptionKey.PublicKey().String(),
	})
	cmd.SetContext(t.Context())
	require.ErrorIs(t, cmd.Execute(), api.ErrAddrNotPublic)
	require.Zero(t, requests.Load())
}

//nolint:paralleltest
func TestRequestTicket_RejectsForeignSignature(t *testing.T) {
	cases := []struct {
		corrupt bool
		err     error
		foreign bool
		name    string
	}{
		{err: auth.ErrInvalidSignature, corrupt: true, name: "corrupted signature"},
		{err: auth.ErrUnauthorized, foreign: true, name: "issuer without grant"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fakeRPC := sdktest.NewFakeRPC(t)
			fixture := newRunnerFixture(t, fakeRPC)
			seedConfigAccount(t, fakeRPC, dvpn.Config{Mint: testMint})

			ownerKey, err := solana.NewRandomPrivateKey()
			require.NoError(t, err)

			operatorKey, err := solana.NewRandomPrivateKey()
			require.NoError(t, err)

			providerKey, err := solana.NewRandomPrivateKey()
			require.NoError(t, err)

			providerAddress := providerKey.PublicKey()

			srv, svc := startProviderAPI(t, func(next http.Handler) http.Handler { return next })
			svc.corruptSignature = tc.corrupt
			svc.issuer = operatorKey

			if tc.foreign {
				svc.issuer, err = solana.NewRandomPrivateKey()
				require.NoError(t, err)
			}

			uri, uriLen := packURI(srv.URL)
			seedProviderAccount(t, fakeRPC, providerAddress, dvpn.Provider{
				Owner:  ownerKey.PublicKey(),
				URI:    uri,
				URILen: uriLen,
			})

			perms := program.PermSignProviderMetadata | program.PermIssueTickets
			seedGrantAccount(t, fakeRPC, ownerKey.PublicKey(), operatorKey.PublicKey(), perms)

			now := time.Now()
			svc.metadata, err = providerapi.SignMetadata(operatorKey, providerapi.Metadata{
				API:        providerapi.APIInfo{URLs: []string{srv.URL}, TLSSHA256: api.Fingerprint(srv.Certificate())},
				ExpiresAt:  now.Add(auth.MetadataTTL).Unix(),
				IssuedAt:   now.Unix(),
				Nodes:      []solana.PublicKey{},
				Operator:   operatorKey.PublicKey(),
				Owner:      ownerKey.PublicKey(),
				Plans:      []solana.PublicKey{},
				ProgramID:  dvpn.ProgramID,
				Provider:   providerAddress,
				SDKVersion: "test-version",
				Version:    auth.MetadataVersion,
			})
			require.NoError(t, err)

			cmd := cli.NewTicketRequestCmd(
				fixture.programClient,
				fixture.signer,
				sdkcli.PeerConfig{AllowPrivate: true},
				fixture.printer.Printer,
			)
			cmd.SetArgs([]string{
				"--provider", providerAddress.String(),
				"--node", solana.NewWallet().PublicKey().String(),
				"--subscription", solana.NewWallet().PublicKey().String(),
			})
			cmd.SetContext(t.Context())
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			require.ErrorIs(t, cmd.Execute(), tc.err)
		})
	}
}

//nolint:paralleltest
func TestRequestTicket_VerifiesMetadataThenIssues(t *testing.T) {
	fakeRPC := sdktest.NewFakeRPC(t)
	fixture := newRunnerFixture(t, fakeRPC)
	chainCfg := dvpn.Config{Mint: testMint}
	seedConfigAccount(t, fakeRPC, chainCfg)

	ownerKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	operatorKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	providerKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	providerAddress := providerKey.PublicKey()

	nodeKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	subscriptionKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	encodedResponse := make(chan string, 1)
	srv, svc := startProviderAPI(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(recordingResponseWriter{
				ResponseWriter: writer,
				encoded:        encodedResponse,
			}, req)
		})
	})
	svc.issuer = operatorKey

	uri, uriLen := packURI(srv.URL)
	provider := dvpn.Provider{
		Owner:  ownerKey.PublicKey(),
		URI:    uri,
		URILen: uriLen,
	}
	seedProviderAccount(t, fakeRPC, providerAddress, provider)

	perms := program.PermSignProviderMetadata | program.PermIssueTickets
	seedGrantAccount(t, fakeRPC, ownerKey.PublicKey(), operatorKey.PublicKey(), perms)

	now := time.Now()
	metadata := providerapi.Metadata{
		API:        providerapi.APIInfo{URLs: []string{srv.URL}, TLSSHA256: api.Fingerprint(srv.Certificate())},
		ExpiresAt:  now.Add(auth.MetadataTTL).Unix(),
		IssuedAt:   now.Unix(),
		Operator:   operatorKey.PublicKey(),
		Owner:      ownerKey.PublicKey(),
		ProgramID:  dvpn.ProgramID,
		Nodes:      []solana.PublicKey{},
		Plans:      []solana.PublicKey{},
		Provider:   providerAddress,
		SDKVersion: "test-version",
		Version:    auth.MetadataVersion,
	}

	signedMetadata, err := providerapi.SignMetadata(operatorKey, metadata)
	require.NoError(t, err)

	svc.metadata = signedMetadata

	cmd := cli.NewTicketRequestCmd(
		fixture.programClient,
		fixture.signer,
		sdkcli.PeerConfig{AllowPrivate: true},
		fixture.printer.Printer,
	)
	cmd.SetArgs([]string{
		"--provider", providerAddress.String(),
		"--node", nodeKey.PublicKey().String(),
		"--subscription", subscriptionKey.PublicKey().String(),
	})
	cmd.SetContext(t.Context())
	require.NoError(t, cmd.Execute())

	var result struct {
		Encoded string          `json:"encoded"`
		Ticket  json.RawMessage `json:"ticket"`
	}
	fixture.printer.decode(t, &result)
	require.Contains(t, string(fixture.printer.last), `"encoded": "`)

	select {
	case encoded := <-encodedResponse:
		require.Equal(t, encoded, result.Encoded)
	default:
		t.Fatal("provider did not return an encoded ticket")
	}

	st, err := auth.DecodeTicket(result.Encoded)
	require.NoError(t, err)

	var expected bytes.Buffer
	require.NoError(t, cli.NewPrinter(&expected, cli.FormatJSON).Print(st))
	require.JSONEq(t, expected.String(), string(result.Ticket))
	require.Equal(t, providerAddress, st.Ticket.Provider)
	require.Equal(t, nodeKey.PublicKey(), st.Ticket.Node)
	require.Equal(t, subscriptionKey.PublicKey(), st.Ticket.Subscription)
	require.Equal(t, operatorKey.PublicKey(), st.Signer)
}

func startProviderAPI(
	t *testing.T,
	wrap func(http.Handler) http.Handler,
) (*httptest.Server, *fakeProviderAPI) {
	t.Helper()

	gin.SetMode(gin.TestMode)

	svc := &fakeProviderAPI{}
	options := api.EngineOptions{Logger: zerolog.Nop()}
	engine := api.NewEngine(options)
	providerapi.RegisterRoutes(engine, svc, api.NewLimiter(100, 100))

	srv := httptest.NewTLSServer(wrap(engine))
	t.Cleanup(srv.Close)

	return srv, svc
}
