package main

import (
	"bytes"
	"fmt"
	"strings"
)

func renderPDAs(p *program) ([]byte, error) {
	specs, _, err := collectPDAs(p.doc)
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer

	fmt.Fprintf(&b, "package %s\n", p.pkg)

	if len(specs) == 0 {
		return b.Bytes(), nil
	}

	var body bytes.Buffer

	prefixed := []pdaSpec{}

	for _, s := range specs {
		if s.prefix != "" {
			prefixed = append(prefixed, s)
		}
	}

	if len(prefixed) > 0 {
		body.WriteString("const (\n")

		for _, s := range prefixed {
			fmt.Fprintf(&body, "\tSeed%s = %q\n", exportedName(s.typeName), s.prefix)
		}

		body.WriteString(")\n\n")
	}

	for _, s := range specs {
		params := []string{}
		program := "programID"

		if s.program == "" {
			params = append(params, "programID solana.PublicKey")
		} else {
			program = s.program
		}

		for _, prm := range s.params {
			params = append(params, prm.name+" "+prm.goType)
		}

		fmt.Fprintf(&body, "func Find%sAddress(%s) (solana.PublicKey, uint8, error) {\n", exportedName(s.typeName), strings.Join(params, ", "))
		fmt.Fprintf(&body, "\treturn solana.FindProgramAddress([][]byte{%s}, %s)\n}\n\n", strings.Join(s.seeds, ", "), program)
	}

	b.WriteString("\nimport (\n")

	if strings.Contains(body.String(), "binary.LittleEndian") {
		b.WriteString("\t\"encoding/binary\"\n\n")
	}

	b.WriteString("\t\"github.com/solana-foundation/solana-go/v2\"\n)\n\n")
	b.Write(body.Bytes())

	return b.Bytes(), nil
}
