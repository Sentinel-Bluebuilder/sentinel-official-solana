package coretest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
)

const maxFilters = 4

var (
	errInvalidParams  = errors.New("invalid params")
	errMethodNotFound = errors.New("method not found")
)

type Account struct {
	Data     []byte
	Lamports uint64
	Owner    solana.PublicKey
}

type Server struct {
	accounts map[solana.PublicKey]Account
	mu       sync.Mutex
	slot     uint64
	srv      *httptest.Server
}

func (s *Server) Client() *rpc.Client {
	return rpc.New(s.srv.URL)
}

func (s *Server) SetAccount(address solana.PublicKey, a Account) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.accounts[address] = a
}

func (s *Server) SetSlot(n uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.slot = n
}

func (s *Server) context() map[string]any {
	return map[string]any{"slot": s.slot}
}

func (s *Server) getAccountInfo(params []json.RawMessage) (any, error) {
	var address solana.PublicKey
	if len(params) == 0 || json.Unmarshal(params[0], &address) != nil {
		return nil, errInvalidParams
	}

	var value any
	if a, ok := s.accounts[address]; ok {
		value = encode(a)
	}

	return map[string]any{"context": s.context(), "value": value}, nil
}

func (s *Server) getMultipleAccounts(params []json.RawMessage) (any, error) {
	var addresses []solana.PublicKey
	if len(params) == 0 || json.Unmarshal(params[0], &addresses) != nil {
		return nil, errInvalidParams
	}

	values := make([]any, 0, len(addresses))

	for _, address := range addresses {
		if a, ok := s.accounts[address]; ok {
			values = append(values, encode(a))
		} else {
			values = append(values, nil)
		}
	}

	return map[string]any{"context": s.context(), "value": values}, nil
}

func (s *Server) getProgramAccounts(params []json.RawMessage) (any, error) {
	var programID solana.PublicKey
	if len(params) == 0 || json.Unmarshal(params[0], &programID) != nil {
		return nil, errInvalidParams
	}

	var opts struct {
		Filters     []rpc.RPCFilter `json:"filters"`
		WithContext *bool           `json:"withContext"`
	}

	if len(params) > 1 {
		if err := json.Unmarshal(params[1], &opts); err != nil {
			return nil, errInvalidParams
		}
	}
	if len(opts.Filters) > maxFilters {
		return nil, errInvalidParams
	}

	addresses := make([]solana.PublicKey, 0, len(s.accounts))

	for address, a := range s.accounts {
		if a.Owner.Equals(programID) && matches(a.Data, opts.Filters) {
			addresses = append(addresses, address)
		}
	}

	sort.Slice(addresses, func(i, j int) bool { return addresses[i].String() < addresses[j].String() })

	out := make([]any, 0, len(addresses))

	for _, address := range addresses {
		out = append(out, map[string]any{"pubkey": address.String(), "account": encode(s.accounts[address])})
	}

	if opts.WithContext != nil && *opts.WithContext {
		return map[string]any{"context": s.context(), "value": out}, nil
	}

	return out, nil
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		result any
		err    error
	)

	switch req.Method {
	case "getAccountInfo":
		result, err = s.getAccountInfo(req.Params)
	case "getMultipleAccounts":
		result, err = s.getMultipleAccounts(req.Params)
	case "getProgramAccounts":
		result, err = s.getProgramAccounts(req.Params)
	default:
		err = errMethodNotFound
	}

	w.Header().Set("Content-Type", "application/json")

	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": err.Error()}})

		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
}

type request struct {
	ID     json.RawMessage   `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

func New(t testing.TB) *Server {
	s := &Server{
		accounts: map[solana.PublicKey]Account{},
		slot:     1,
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)

	return s
}

func encode(a Account) map[string]any {
	return map[string]any{
		"lamports":   a.Lamports,
		"owner":      a.Owner.String(),
		"data":       []string{base64.StdEncoding.EncodeToString(a.Data), "base64"},
		"executable": false,
		"rentEpoch":  0,
	}
}

func matches(data []byte, filters []rpc.RPCFilter) bool {
	for _, f := range filters {
		if f.DataSize != 0 && uint64(len(data)) != f.DataSize {
			return false
		}

		if f.Memcmp != nil {
			if f.Memcmp.Offset > uint64(len(data)) || uint64(len(f.Memcmp.Bytes)) > uint64(len(data))-f.Memcmp.Offset {
				return false
			}

			end := f.Memcmp.Offset + uint64(len(f.Memcmp.Bytes))
			if !bytes.Equal(data[f.Memcmp.Offset:end], f.Memcmp.Bytes) {
				return false
			}
		}
	}

	return true
}
