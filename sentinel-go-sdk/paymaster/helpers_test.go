package paymaster_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/programs/system"
	"github.com/stretchr/testify/require"
)

type fakeKora struct {
	apiKeys   []string
	params    map[string]map[string]any
	responses map[string]string
	status    int
}

func (f *fakeKora) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}

	_ = json.NewDecoder(r.Body).Decode(&req)

	f.apiKeys = r.Header.Values("X-Api-Key")
	f.params[req.Method] = req.Params

	w.WriteHeader(f.status)
	_, _ = w.Write([]byte(f.responses[req.Method]))
}

func newResponse(t *testing.T, result any) string {
	t.Helper()

	body, err := json.Marshal(map[string]any{"id": 1, "jsonrpc": "2.0", "result": result})
	require.NoError(t, err)

	return string(body)
}

func newTransaction(t *testing.T, payer solana.PublicKey, ixs ...solana.Instruction) *solana.Transaction {
	t.Helper()

	if len(ixs) == 0 {
		ixs = []solana.Instruction{system.NewTransferInstruction(1, payer, payer).Build()}
	}

	cfg := solana.TransactionConfig{}.WithComputeUnitLimit(200_000).WithLoadedAccountsDataSizeLimit(65_536)

	transaction, err := solana.NewTransaction(
		ixs,
		solana.Hash{},
		solana.TransactionPayer(payer),
		solana.TransactionV1Config(cfg),
	)
	require.NoError(t, err)

	return transaction
}

func startFakeKora(t *testing.T, responses map[string]string) (*fakeKora, string) {
	t.Helper()

	fakeKora := &fakeKora{
		params:    map[string]map[string]any{},
		responses: responses,
		status:    http.StatusOK,
	}
	server := httptest.NewServer(fakeKora)
	t.Cleanup(server.Close)

	return fakeKora, server.URL
}
