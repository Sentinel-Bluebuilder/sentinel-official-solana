package main

import (
	"bytes"
	"fmt"
)

const errorsBody = `import (
	"encoding/json"
	"errors"
	"strconv"

	"github.com/solana-foundation/solana-go/v2/rpc/jsonrpc"
)

type Error struct {
	Code    uint32
	Name    string
	Message string
}

func (e *Error) Error() string {
	return e.Name + " (" + strconv.FormatUint(uint64(e.Code), 10) + "): " + e.Message
}

func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)

	return ok && t != nil && e != nil && t.Code == e.Code
}

func ErrorByCode(code uint32) (*Error, bool) {
	e, ok := errorsByCode[code]

	return e, ok
}

func ErrorFromRPCError(err error) (*Error, bool) {
	var rpcErr *jsonrpc.RPCError
	if !errors.As(err, &rpcErr) || rpcErr == nil {
		return nil, false
	}

	data, ok := rpcErr.Data.(map[string]any)
	if !ok {
		return nil, false
	}

	return ErrorFromValue(data["err"])
}

func ErrorFromValue(v any) (*Error, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}

	ie, ok := m["InstructionError"].([]any)
	if !ok || len(ie) != 2 {
		return nil, false
	}

	if _, ok := toUint32(ie[0]); !ok {
		return nil, false
	}

	custom, ok := ie[1].(map[string]any)
	if !ok {
		return nil, false
	}

	code, ok := toUint32(custom["Custom"])
	if !ok {
		return nil, false
	}

	return ErrorByCode(code)
}

func toUint32(v any) (uint32, bool) {
	switch n := v.(type) {
	case json.Number:
		u, err := strconv.ParseUint(n.String(), 10, 32)

		return uint32(u), err == nil
	case float64:
		if n < 0 || n > 1<<32-1 || n != float64(uint32(n)) {
			return 0, false
		}

		return uint32(n), true
	}

	return 0, false
}

`

func renderErrors(p *program) ([]byte, error) {
	var b bytes.Buffer

	fmt.Fprintf(&b, "package %s\n\n%s", p.pkg, errorsBody)

	if len(p.doc.Errors) > 0 {
		b.WriteString("var (\n")

		for _, e := range p.doc.Errors {
			fmt.Fprintf(&b, "\tErr%s = &Error{Code: %d, Name: %q, Message: %q}\n", exportedName(e.Name), e.Code, e.Name, e.Msg.UnwrapOr(""))
		}

		b.WriteString(")\n\n")
	}

	b.WriteString("var errorsByCode = map[uint32]*Error{\n")

	for _, e := range p.doc.Errors {
		fmt.Fprintf(&b, "\t%d: Err%s,\n", e.Code, exportedName(e.Name))
	}

	b.WriteString("}\n")

	return b.Bytes(), nil
}
