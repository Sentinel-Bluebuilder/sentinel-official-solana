package main

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
)

// filterName names the filter of an account field, dropping the account name
// the field name repeats.
func filterName(account, field string) string {
	name := exportedName(field)

	if rest, ok := strings.CutPrefix(name, account); ok && rest != "" && !unicode.IsLower([]rune(rest)[0]) {
		name = rest
	}

	return account + name + "Filter"
}

func renderFilters(p *program) ([]byte, error) {
	layouts, err := accountLayouts(p.doc)
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer

	fmt.Fprintf(&b, "package %s\n", p.pkg)

	if len(layouts) == 0 {
		return b.Bytes(), nil
	}

	var body bytes.Buffer

	sized := []accountLayout{}

	for _, l := range layouts {
		if l.fixedSize {
			sized = append(sized, l)
		}
	}

	if len(sized) > 0 {
		body.WriteString("const (\n")

		for _, l := range sized {
			fmt.Fprintf(&body, "\t%sAccountSizeBytes = %d\n", exportedName(l.name), l.sizeBytes)
		}

		body.WriteString(")\n\n")
	}

	for _, l := range layouts {
		name := exportedName(l.name)
		disc := name + "AccountDiscriminator"

		fmt.Fprintf(&body, "func %sFilter() rpc.RPCFilter {\n\treturn rpc.RPCFilter{Memcmp: &rpc.RPCFilterMemcmp{Offset: 0, Bytes: %s[:]}}\n}\n\n", name, disc)

		for _, f := range l.fields {
			expr, ok := filterBytesExpr(f.goType, "v")
			if !ok {
				continue
			}

			fmt.Fprintf(&body, "func %s(v %s) rpc.RPCFilter {\n\treturn rpc.RPCFilter{Memcmp: &rpc.RPCFilterMemcmp{Offset: %d, Bytes: %s}}\n}\n\n", filterName(name, f.name), f.goType, f.offset, expr)
		}
	}

	if strings.Contains(body.String(), "boolByte(") {
		body.WriteString("func boolByte(b bool) byte {\n\tif b {\n\t\treturn 1\n\t}\n\n\treturn 0\n}\n")
	}

	b.WriteString("\nimport (\n")

	if strings.Contains(body.String(), "binary.LittleEndian") {
		b.WriteString("\t\"encoding/binary\"\n\n")
	}

	if strings.Contains(body.String(), "solana.") {
		b.WriteString("\t\"github.com/solana-foundation/solana-go/v2\"\n")
	}

	b.WriteString("\t\"github.com/solana-foundation/solana-go/v2/rpc\"\n)\n\n")
	b.Write(body.Bytes())

	return b.Bytes(), nil
}
