package main

import (
	"bytes"
	"fmt"
	"strings"
)

func renderBuilders(p *program) ([]byte, error) {
	_, pdas, err := collectPDAs(p.doc)
	if err != nil {
		return nil, err
	}

	_, addresses, err := collectAddresses(p)
	if err != nil {
		return nil, err
	}

	var body bytes.Buffer

	for i := range p.doc.Instructions {
		ix := &p.doc.Instructions[i]

		b, err := planBuilder(p, ix, pdas, addresses)
		if err != nil {
			return nil, err
		}

		for _, a := range b.args {
			if a.field == "RemainingAccounts" || exportedName(a.name) == "RemainingAccounts" {
				return nil, fmt.Errorf("%s: argument %s collides with the RemainingAccounts field", ix.Name, a.name)
			}
		}

		for _, acc := range b.accounts {
			if acc.field == "RemainingAccounts" || exportedName(acc.acc.Name) == "RemainingAccounts" {
				return nil, fmt.Errorf("%s: account %s collides with the RemainingAccounts field", ix.Name, acc.acc.Name)
			}
		}

		writeBuilder(&body, b)
	}

	var b bytes.Buffer

	if body.Len() == 0 {
		return []byte(fmt.Sprintf("package %s\n\n", p.pkg)), nil
	}

	fmt.Fprintf(&b, "package %s\n\nimport (\n", p.pkg)
	b.WriteString("\t\"" + modulePath + "/core\"\n")

	if strings.Contains(body.String(), "bin.") {
		b.WriteString("\tbin \"github.com/gagliardetto/binary\"\n")
	}

	b.WriteString("\t\"github.com/solana-foundation/solana-go/v2\"\n)\n\n")
	b.Write(body.Bytes())

	return b.Bytes(), nil
}

func writeBuilder(w *bytes.Buffer, b *builder) {
	name := exportedName(b.ix.Name)

	fmt.Fprintf(w, "type %sParams struct {\n", name)

	for _, a := range b.args {
		fmt.Fprintf(w, "\t%s %s\n", a.field, a.goType)
	}

	for _, acc := range b.accounts {
		switch acc.kind {
		case accountKindParam:
			fmt.Fprintf(w, "\t%s solana.PublicKey\n", acc.field)
		case accountKindOptional:
			fmt.Fprintf(w, "\t%s *solana.PublicKey\n", acc.field)
		}
	}

	w.WriteString("\tRemainingAccounts []*solana.AccountMeta\n")

	fmt.Fprintf(w, "}\n\nfunc Build%sInstruction(programID solana.PublicKey, p %sParams) (solana.Instruction, error) {\n", name, name)

	for _, acc := range b.accounts {
		if acc.kind == accountKindOptional {
			fmt.Fprintf(w, "\t%s := programID\n\tif p.%s != nil {\n\t\t%s = *p.%s\n\t}\n\n", acc.local, acc.field, acc.local, acc.field)
		}
	}

	for _, i := range b.derived {
		acc := b.accounts[i]
		fmt.Fprintf(w, "\t%s, _, err := %s\n\tif err != nil {\n\t\treturn nil, err\n\t}\n\n", acc.local, acc.find)
	}

	call := []string{}

	for _, a := range b.args {
		call = append(call, "p."+a.field)
	}

	for _, acc := range b.accounts {
		call = append(call, acc.expr)
	}

	fmt.Fprintf(w, "\tix, err := new%sInstruction(%s)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n\n", name, strings.Join(call, ", "))
	w.WriteString("\treturn core.WithProgramID(ix, programID, p.RemainingAccounts), nil\n}\n\n")
}
