package paymaster_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/paymaster"
)

func TestClient_EstimateTransactionFee(t *testing.T) {
	t.Parallel()

	feeToken, feePayer := solana.NewWallet().PublicKey(), solana.NewWallet().PublicKey()
	paymentAddress, signer := solana.NewWallet().PublicKey(), solana.NewWallet().PublicKey()
	transaction := newTransaction(t, solana.NewWallet().PublicKey())
	feeBaseUnits := uint64(2500)
	result := map[string]any{
		"fee_in_lamports": 5000,
		"fee_in_token":    feeBaseUnits,
		"payment_address": paymentAddress,
		"signer_pubkey":   signer,
	}
	fakeKora, url := startFakeKora(t, map[string]string{"estimateTransactionFee": newResponse(t, result)})
	client := paymaster.NewClient(url, "")

	estimate, err := client.EstimateTransactionFee(t.Context(), transaction, feeToken, feePayer)
	require.NoError(t, err)

	want := paymaster.Estimate{
		FeeBaseUnits:   &feeBaseUnits,
		FeeLamports:    5000,
		PaymentAddress: paymentAddress,
		Signer:         signer,
	}
	require.Equal(t, want, estimate)

	wantParams := map[string]any{
		"fee_token":   feeToken.String(),
		"sig_verify":  false,
		"signer_key":  feePayer.String(),
		"transaction": transaction.MustToBase64(),
	}
	require.Equal(t, wantParams, fakeKora.params["estimateTransactionFee"])

	delete(result, "fee_in_token")
	fakeKora.responses["estimateTransactionFee"] = newResponse(t, result)

	estimate, err = client.EstimateTransactionFee(t.Context(), transaction, feeToken, feePayer)
	require.NoError(t, err)
	require.Nil(t, estimate.FeeBaseUnits)

	fakeKora.responses["estimateTransactionFee"] = newResponse(t, map[string]any{"fee_in_lamports": 1})

	_, err = client.EstimateTransactionFee(t.Context(), transaction, feeToken, feePayer)
	require.ErrorContains(t, err, "paymaster estimateTransactionFee: parsing payment_address")
}

func TestClient_PayerSigner(t *testing.T) {
	t.Parallel()

	paymentAddress, signer := solana.NewWallet().PublicKey(), solana.NewWallet().PublicKey()
	result := map[string]any{"payment_address": paymentAddress, "signer_address": signer}
	fakeKora, url := startFakeKora(t, map[string]string{"getPayerSigner": newResponse(t, result)})

	payerSigner, err := paymaster.NewClient(url, "secret").PayerSigner(t.Context())
	require.NoError(t, err)
	require.Equal(t, paymaster.PayerSigner{PaymentAddress: paymentAddress, Signer: signer}, payerSigner)
	require.Equal(t, []string{"secret"}, fakeKora.apiKeys)

	_, err = paymaster.NewClient(url, "").PayerSigner(t.Context())
	require.NoError(t, err)
	require.Empty(t, fakeKora.apiKeys)
}

func TestClient_PayerSigner_RefusesRedirect(t *testing.T) {
	t.Parallel()

	var redirected atomic.Bool

	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Store(true) }))
	t.Cleanup(target.Close)

	server := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusTemporaryRedirect))
	t.Cleanup(server.Close)

	_, err := paymaster.NewClient(server.URL, "secret").PayerSigner(t.Context())
	require.ErrorIs(t, err, api.ErrUnexpectedRedirect)
	require.False(t, redirected.Load())
}

func TestClient_PayerSigner_ReportsFailures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		body    string
		name    string
		status  int
		wantErr string
	}{
		{
			body:    `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"denied"}}`,
			name:    "rpc error",
			status:  http.StatusOK,
			wantErr: "paymaster getPayerSigner: denied (code -32000)",
		},
		{
			body:    "bad gateway",
			name:    "http status",
			status:  http.StatusBadGateway,
			wantErr: "paymaster getPayerSigner: unexpected status 502",
		},
		{
			body:    `{"result":"` + strings.Repeat("a", 1<<20) + `"}`,
			name:    "oversized response",
			status:  http.StatusOK,
			wantErr: "paymaster getPayerSigner: response exceeds 1048576 bytes",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fakeKora, url := startFakeKora(t, map[string]string{"getPayerSigner": tc.body})
			fakeKora.status = tc.status

			_, err := paymaster.NewClient(url, "").PayerSigner(t.Context())
			require.EqualError(t, err, tc.wantErr)
		})
	}
}

func TestClient_Price(t *testing.T) {
	t.Parallel()

	cases := []struct {
		free bool
		name string
	}{
		{free: true, name: "free"},
		{name: "margin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := map[string]any{"validation_config": map[string]any{"price": map[string]any{"type": tc.name}}}
			_, url := startFakeKora(t, map[string]string{"getConfig": newResponse(t, result)})

			price, err := paymaster.NewClient(url, "").Price(t.Context())
			require.NoError(t, err)
			require.Equal(t, paymaster.Price{Type: tc.name}, price)
			require.Equal(t, tc.free, price.Free())
		})
	}
}

func TestClient_SignTransaction(t *testing.T) {
	t.Parallel()

	feePayer := solana.NewWallet()
	transaction := newTransaction(t, feePayer.PublicKey())
	signed := newTransaction(t, feePayer.PublicKey())

	_, err := signed.Sign(func(solana.PublicKey) *solana.PrivateKey { return &feePayer.PrivateKey })
	require.NoError(t, err)

	result := map[string]any{"signed_transaction": signed.MustToBase64()}
	fakeKora, url := startFakeKora(t, map[string]string{"signTransaction": newResponse(t, result)})
	client := paymaster.NewClient(url, "")

	returned, err := client.SignTransaction(t.Context(), transaction, feePayer.PublicKey())
	require.NoError(t, err)
	require.Equal(t, signed.MustToBase64(), returned.MustToBase64())

	wantParams := map[string]any{
		"sig_verify":  false,
		"signer_key":  feePayer.PublicKey().String(),
		"transaction": transaction.MustToBase64(),
	}
	require.Equal(t, wantParams, fakeKora.params["signTransaction"])

	result["lighthouse_assertion_added"] = true
	fakeKora.responses["signTransaction"] = newResponse(t, result)

	_, err = client.SignTransaction(t.Context(), transaction, feePayer.PublicKey())
	require.ErrorContains(t, err, "disable kora's lighthouse")

	fakeKora.responses["signTransaction"] = newResponse(t, map[string]any{})

	_, err = client.SignTransaction(t.Context(), transaction, feePayer.PublicKey())
	require.ErrorContains(t, err, "paymaster signTransaction: decoding signed_transaction")
}
