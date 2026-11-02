package main

import (
	"bytes"
	"fmt"
)

const logsErrors = `func ParseProgramError(logs []string) error {
	return core.ParseProgramError(logs, func(code uint32) (error, bool) {
		e, ok := ErrorByCode(code)
		if !ok {
			return nil, false
		}

		return e, true
	})
}
`

const logsEvents = `func ParseEvents(logs []string) []any {
	return core.ParseEvents(logs, ParseAnyEvent)
}

func StreamEvents(ctx context.Context, wsClient *ws.Client, programID solana.PublicKey, commitment rpc.CommitmentType) (<-chan core.Event, error) {
	return core.StreamEvents(ctx, wsClient, programID, commitment, ParseAnyEvent)
}

`

func renderLogs(p *program) ([]byte, error) {
	var b bytes.Buffer

	fmt.Fprintf(&b, "package %s\n\nimport (\n", p.pkg)

	if len(p.doc.Events) > 0 {
		b.WriteString("\t\"context\"\n\n")
	}

	fmt.Fprintf(&b, "\t%q\n", modulePath+"/core")

	if len(p.doc.Events) > 0 {
		b.WriteString("\t\"github.com/solana-foundation/solana-go/v2\"\n\t\"github.com/solana-foundation/solana-go/v2/rpc\"\n\t\"github.com/solana-foundation/solana-go/v2/rpc/ws\"\n")
	}

	b.WriteString(")\n\n")

	if len(p.doc.Events) > 0 {
		b.WriteString(logsEvents)
	}

	b.WriteString(logsErrors)

	return b.Bytes(), nil
}
