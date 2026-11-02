package core_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/solana-foundation/solana-go/v2/rpc/ws"
)

func TestWatch(t *testing.T) {
	data := base64.StdEncoding.EncodeToString([]byte{9})
	notification := fmt.Sprintf(`{"context":{"slot":5},"value":{"lamports":1,"owner":%q,"data":[%q,"base64"],"executable":false,"rentEpoch":0}}`, owner, data)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	ch, err := core.Watch(ctx, startFakeWSServer(t, notification), address, core.Options{Owner: owner}, fakeDecodeFirstByte)
	if err != nil {
		t.Fatal(err)
	}

	got := <-ch
	if got.Address != address || got.Slot != 5 || got.Value != 9 {
		t.Fatalf("got %+v", got)
	}

	cancel()

	if _, ok := <-ch; ok {
		t.Fatal("channel must close after cancel")
	}
}

func startFakeWSServer(t *testing.T, results ...string) *ws.Client {
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)

			return
		}
		defer conn.Close()

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var req struct {
			ID     uint64 `json:"id"`
			Method string `json:"method"`
		}

		if err := json.Unmarshal(msg, &req); err != nil {
			t.Error(err)

			return
		}

		if err := conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","result":1,"id":%d}`, req.ID))); err != nil {
			return
		}

		method := strings.TrimSuffix(req.Method, "Subscribe") + "Notification"

		for _, notification := range results {
			if err := conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":%q,"params":{"result":%s,"subscription":1}}`, method, notification))); err != nil {
				return
			}
		}

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	wsClient, err := ws.Connect(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wsClient.Close)

	return wsClient
}
