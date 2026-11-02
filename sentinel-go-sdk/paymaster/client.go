package paymaster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/solana-foundation/solana-go/v2"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
)

const (
	maxResponseBytes = 1 << 20
	requestTimeout   = 10 * time.Second
)

//nolint:gochecknoglobals
var httpClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return api.ErrUnexpectedRedirect },
	Timeout:       requestTimeout,
}

type Client struct {
	apiKey string
	url    string
}

func NewClient(url, apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		url:    url,
	}
}

func (c *Client) EstimateTransactionFee(
	ctx context.Context,
	t *solana.Transaction,
	feeToken, feePayer solana.PublicKey,
) (Estimate, error) {
	encoded, err := t.ToBase64()
	if err != nil {
		return Estimate{}, fmt.Errorf("paymaster estimateTransactionFee: encoding transaction: %w", err)
	}

	params := map[string]any{
		"fee_token":   feeToken.String(),
		"sig_verify":  false,
		"signer_key":  feePayer.String(),
		"transaction": encoded,
	}

	var result struct {
		FeeBaseUnits   *uint64 `json:"fee_in_token"`
		FeeLamports    uint64  `json:"fee_in_lamports"`
		PaymentAddress string  `json:"payment_address"`
		Signer         string  `json:"signer_pubkey"`
	}

	if err := c.call(ctx, "estimateTransactionFee", params, &result); err != nil {
		return Estimate{}, err
	}

	paymentAddress, err := solana.PublicKeyFromBase58(result.PaymentAddress)
	if err != nil {
		return Estimate{}, fmt.Errorf("paymaster estimateTransactionFee: parsing payment_address: %w", err)
	}

	signer, err := solana.PublicKeyFromBase58(result.Signer)
	if err != nil {
		return Estimate{}, fmt.Errorf("paymaster estimateTransactionFee: parsing signer_pubkey: %w", err)
	}

	estimate := Estimate{
		FeeBaseUnits:   result.FeeBaseUnits,
		FeeLamports:    result.FeeLamports,
		PaymentAddress: paymentAddress,
		Signer:         signer,
	}

	return estimate, nil
}

func (c *Client) PayerSigner(ctx context.Context) (PayerSigner, error) {
	var result struct {
		PaymentAddress string `json:"payment_address"`
		Signer         string `json:"signer_address"`
	}

	if err := c.call(ctx, "getPayerSigner", map[string]any{}, &result); err != nil {
		return PayerSigner{}, err
	}

	paymentAddress, err := solana.PublicKeyFromBase58(result.PaymentAddress)
	if err != nil {
		return PayerSigner{}, fmt.Errorf("paymaster getPayerSigner: parsing payment_address: %w", err)
	}

	signer, err := solana.PublicKeyFromBase58(result.Signer)
	if err != nil {
		return PayerSigner{}, fmt.Errorf("paymaster getPayerSigner: parsing signer_address: %w", err)
	}

	payerSigner := PayerSigner{
		PaymentAddress: paymentAddress,
		Signer:         signer,
	}

	return payerSigner, nil
}

func (c *Client) Price(ctx context.Context) (Price, error) {
	var result struct {
		ValidationConfig struct {
			Price Price `json:"price"`
		} `json:"validation_config"`
	}

	if err := c.call(ctx, "getConfig", map[string]any{}, &result); err != nil {
		return Price{}, err
	}

	return result.ValidationConfig.Price, nil
}

func (c *Client) SignTransaction(
	ctx context.Context,
	t *solana.Transaction,
	feePayer solana.PublicKey,
) (*solana.Transaction, error) {
	encoded, err := t.ToBase64()
	if err != nil {
		return nil, fmt.Errorf("paymaster signTransaction: encoding transaction: %w", err)
	}

	params := map[string]any{
		"sig_verify":  false,
		"signer_key":  feePayer.String(),
		"transaction": encoded,
	}

	var result struct {
		LighthouseAssertionAdded bool   `json:"lighthouse_assertion_added"`
		SignedTransaction        string `json:"signed_transaction"`
	}

	if err := c.call(ctx, "signTransaction", params, &result); err != nil {
		return nil, err
	}

	if result.LighthouseAssertionAdded {
		return nil, errors.New("paymaster signTransaction: lighthouse assertion added, disable kora's lighthouse")
	}

	signed, err := solana.TransactionFromBase64(result.SignedTransaction)
	if err != nil {
		return nil, fmt.Errorf("paymaster signTransaction: decoding signed_transaction: %w", err)
	}

	return signed, nil
}

func (c *Client) call(ctx context.Context, method string, params map[string]any, result any) error {
	status, body, err := c.post(ctx, method, params)
	if err != nil {
		return fmt.Errorf("paymaster %s: %w", method, err)
	}

	var response rpcResponse

	decodeErr := json.Unmarshal(body, &response)

	switch {
	case decodeErr == nil && response.Error != nil:
		return fmt.Errorf("paymaster %s: %s (code %d)", method, response.Error.Message, response.Error.Code)
	case status < http.StatusOK || status >= http.StatusMultipleChoices:
		return fmt.Errorf("paymaster %s: %w", method, &statusError{status: status})
	case decodeErr != nil:
		return fmt.Errorf("paymaster %s: decoding response: %w", method, decodeErr)
	}

	if err := json.Unmarshal(response.Result, result); err != nil {
		return fmt.Errorf("paymaster %s: decoding result: %w", method, err)
	}

	return nil
}

func (c *Client) post(ctx context.Context, method string, params map[string]any) (int, []byte, error) {
	payload, err := json.Marshal(rpcRequest{
		ID:      1,
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return 0, nil, fmt.Errorf("encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, fmt.Errorf("building request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	if c.apiKey != "" {
		req.Header.Set("X-Api-Key", c.apiKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			urlErr.URL = cli.RedactURL(urlErr.URL)
		}

		return 0, nil, fmt.Errorf("sending request: %w", err)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	_ = resp.Body.Close()

	if err != nil {
		return 0, nil, fmt.Errorf("reading response: %w", err)
	}

	if len(body) > maxResponseBytes {
		return 0, nil, fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
	}

	return resp.StatusCode, body, nil
}

type Estimate struct {
	FeeBaseUnits   *uint64
	FeeLamports    uint64
	PaymentAddress solana.PublicKey
	Signer         solana.PublicKey
}

type PayerSigner struct {
	PaymentAddress solana.PublicKey
	Signer         solana.PublicKey
}

type Price struct {
	Type string `json:"type"`
}

func (p Price) Free() bool {
	return p.Type == "free"
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcRequest struct {
	ID      int            `json:"id"`
	JSONRPC string         `json:"jsonrpc"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

type rpcResponse struct {
	Error  *rpcError       `json:"error"`
	Result json.RawMessage `json:"result"`
}

type statusError struct {
	status int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("%v %d", api.ErrUnexpectedStatus, e.status)
}

func (e *statusError) Unwrap() error {
	return api.ErrUnexpectedStatus
}
