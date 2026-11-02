package core

import (
	"context"
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/solana-foundation/solana-go/v2/rpc/ws"
)

const programDataPrefix = "Program data: "

var (
	anchorErrorNumber = regexp.MustCompile(`Error Number: (\d+)`)
	customErrorCode   = regexp.MustCompile(`custom program error: 0x([0-9a-fA-F]+)`)
)

type Event struct {
	Signature solana.Signature
	Slot      uint64
	Value     any
}

func ParseEvents(logs []string, parse func([]byte) (any, error)) []any {
	var out []any

	for _, line := range logs {
		if !strings.HasPrefix(line, programDataPrefix) {
			continue
		}

		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, programDataPrefix))
		if err != nil {
			continue
		}

		v, err := parse(raw)
		if err != nil {
			continue
		}

		out = append(out, v)
	}

	return out
}

func ParseProgramError(logs []string, byCode func(uint32) (error, bool)) error {
	for _, line := range logs {
		if match := anchorErrorNumber.FindStringSubmatch(line); match != nil {
			if err, ok := lookupCode(match[1], 10, byCode); ok {
				return err
			}
		}

		if match := customErrorCode.FindStringSubmatch(line); match != nil {
			if err, ok := lookupCode(match[1], 16, byCode); ok {
				return err
			}
		}
	}

	return nil
}

func StreamEvents(
	ctx context.Context,
	wsClient *ws.Client,
	programID solana.PublicKey,
	commitment rpc.CommitmentType,
	parse func([]byte) (any, error),
) (<-chan Event, error) {
	sub, err := wsClient.LogsSubscribeMentions(programID, commitment)
	if err != nil {
		return nil, err
	}

	out := make(chan Event)

	go func() {
		defer close(out)
		defer sub.Unsubscribe()

		for {
			result, err := sub.Recv(ctx)
			if err != nil {
				return
			}

			if result.Value.Err != nil {
				continue
			}

			for _, v := range ParseEvents(result.Value.Logs, parse) {
				select {
				case out <- Event{
					Signature: result.Value.Signature,
					Slot:      result.Context.Slot,
					Value:     v,
				}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, nil
}

func lookupCode(s string, base int, byCode func(uint32) (error, bool)) (error, bool) {
	code, err := strconv.ParseUint(s, base, 32)
	if err != nil {
		return nil, false
	}

	return byCode(uint32(code))
}
