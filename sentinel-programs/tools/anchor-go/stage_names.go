package main

import (
	"bytes"
	"fmt"
)

type nameEntry struct {
	ident string
	name  string
}

type nameTable struct {
	entries []nameEntry
	kind    string
}

func renderNames(p *program) ([]byte, error) {
	tables := []nameTable{{kind: "Instruction"}, {kind: "Account"}, {kind: "Event"}}

	for _, ix := range p.doc.Instructions {
		tables[0].entries = append(tables[0].entries, nameEntry{
			ident: exportedName(ix.Name) + "InstructionDiscriminator",
			name:  ix.Name,
		})
	}

	for _, acc := range p.doc.Accounts {
		tables[1].entries = append(tables[1].entries, nameEntry{
			ident: exportedName(acc.Name) + "AccountDiscriminator",
			name:  acc.Name,
		})
	}

	for _, ev := range p.doc.Events {
		tables[2].entries = append(tables[2].entries, nameEntry{
			ident: exportedName(ev.Name) + "EventDiscriminator",
			name:  ev.Name,
		})
	}

	var b bytes.Buffer

	fmt.Fprintf(&b, "package %s\n\n", p.pkg)

	for _, t := range tables {
		fmt.Fprintf(&b, "var %sNames = map[[8]byte]string{\n", unexportedName(t.kind))

		for _, e := range t.entries {
			fmt.Fprintf(&b, "\t%s: %q,\n", e.ident, e.name)
		}

		fmt.Fprintf(&b, "}\n\nfunc %sName(discriminator [8]byte) (string, bool) {\n\tname, ok := %sNames[discriminator]\n\n\treturn name, ok\n}\n\n", t.kind, unexportedName(t.kind))
	}

	return b.Bytes(), nil
}
