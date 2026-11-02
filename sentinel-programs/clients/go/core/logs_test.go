package core_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
)

var errKnown = errors.New("known")

func TestParseEvents(t *testing.T) {
	logs := []string{"Program log: hello", "Program data: b2s=", "Program data: !!!", "Program data: YmFk", "Program data: dHdv"}

	got := core.ParseEvents(logs, fakeParseEvent)
	if len(got) != 2 || got[0] != "ok" || got[1] != "two" {
		t.Fatalf("got %v", got)
	}

	if got := core.ParseEvents([]string{"Program log: none"}, fakeParseEvent); got != nil {
		t.Fatalf("got %v", got)
	}
}

func TestParseProgramError(t *testing.T) {
	anchor := []string{"Program log: AnchorError occurred. Error Code: Unauthorized. Error Number: 6000. Error Message: nope."}
	if err := core.ParseProgramError(anchor, fakeErrorByCode); !errors.Is(err, errKnown) {
		t.Fatalf("anchor: %v", err)
	}

	custom := []string{"Program SentineLDVPN1111111111111111111111111111111 failed: custom program error: 0x1770"}
	if err := core.ParseProgramError(custom, fakeErrorByCode); !errors.Is(err, errKnown) {
		t.Fatalf("custom: %v", err)
	}

	if err := core.ParseProgramError([]string{"Error Number: 3012.", "custom program error: 0x1"}, fakeErrorByCode); err != nil {
		t.Fatalf("unknown codes: %v", err)
	}
}

func TestStreamEvents(t *testing.T) {
	sig := solana.Signature{}
	ok := fmt.Sprintf(`{"context":{"slot":9},"value":{"signature":%q,"err":null,"logs":["Program data: b2s="]}}`, sig)
	failed := fmt.Sprintf(`{"context":{"slot":10},"value":{"signature":%q,"err":{"InstructionError":[0,{"Custom":6000}]},"logs":["Program data: dHdv"]}}`, sig)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := core.StreamEvents(ctx, startFakeWSServer(t, failed, ok), owner, rpc.CommitmentConfirmed, fakeParseEvent)
	if err != nil {
		t.Fatal(err)
	}

	got := <-ch
	if got.Signature != sig || got.Slot != 9 || got.Value != "ok" {
		t.Fatalf("got %+v", got)
	}

	cancel()

	if _, ok := <-ch; ok {
		t.Fatal("channel must close after cancel")
	}
}

func fakeErrorByCode(code uint32) (error, bool) {
	if code == 6000 {
		return errKnown, true
	}

	return nil, false
}

func fakeParseEvent(b []byte) (any, error) {
	if string(b) == "bad" {
		return nil, errors.New("bad")
	}

	return string(b), nil
}
