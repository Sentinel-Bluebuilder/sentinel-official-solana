package main

import (
	"bytes"
	"fmt"
)

const resolversPrelude = `import (
	"context"
	"fmt"

	"%s/core"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
)

`

func renderResolvers(p *program) ([]byte, error) {
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

		resolutions, err := collectResolutions(p, b, pdas)
		if err != nil {
			return nil, err
		}

		if len(resolutions) == 0 {
			continue
		}

		resolutions, err = orderResolutions(b, resolutions)
		if err != nil {
			return nil, err
		}

		if err := writeResolver(&body, b, resolutions, pdas); err != nil {
			return nil, err
		}
	}

	var b bytes.Buffer

	fmt.Fprintf(&b, "package %s\n", p.pkg)

	if body.Len() == 0 {
		return b.Bytes(), nil
	}

	fmt.Fprintf(&b, "\n"+resolversPrelude, modulePath)
	b.Write(body.Bytes())

	return b.Bytes(), nil
}

func writeResolver(w *bytes.Buffer, b *builder, resolutions []resolution, pdas map[string]pdaSpec) error {
	name := exportedName(b.ix.Name)
	known := resolverKnown(b)
	locals := map[string]bool{
		"p": true, "programID": true, "ctx": true, "rpcClient": true, "opts": true, "err": true,
		"context": true, "fmt": true, "core": true, "solana": true, "rpc": true,
		"bool": true, "error": true, "true": true, "nil": true,
	}
	allocate := func(name, suffix string) string {
		for locals[name] {
			name += suffix
		}

		locals[name] = true

		return name
	}

	index := map[string]int{}
	for i, ba := range b.accounts {
		index[ba.acc.Name] = i
	}

	fmt.Fprintf(w, "func Resolve%[1]sParams(ctx context.Context, rpcClient *rpc.Client, opts core.Options, programID solana.PublicKey, p %[1]sParams) (%[1]sParams, error) {\n", name)

	type sourceCache struct {
		local  string
		loaded string
	}

	caches := map[string]sourceCache{}
	for _, f := range resolutions {
		if _, ok := caches[f.source]; ok {
			continue
		}

		local := allocate(unexportedName(f.source), "Account")
		loaded := allocate(local+"Loaded", "Account")
		caches[f.source] = sourceCache{
			local:  local,
			loaded: loaded,
		}
		fmt.Fprintf(w, "\tvar %s core.Keyed[%s]\n\tvar %s bool\n", local, exportedName(f.sourceType), loaded)
	}

	w.WriteString("\n")

	for _, f := range resolutions {
		target := b.accounts[f.target]
		srcField := b.accounts[index[f.source]].field
		cache := caches[f.source]

		fmt.Fprintf(w, "\tif p.%s.IsZero() {\n\t\tif !%s {\n\t\t\tif p.%s.IsZero() {\n\t\t\t\treturn p, fmt.Errorf(\"%%w: %s: %s\", core.ErrMissingParam)\n\t\t\t}\n\n", target.field, cache.loaded, srcField, b.ix.Name, f.source)
		fmt.Fprintf(w, "\t\t\tvar err error\n\t\t\t%s, err = Fetch%s(ctx, rpcClient, p.%s, opts)\n\t\t\tif err != nil {\n\t\t\t\treturn p, err\n\t\t\t}\n\n\t\t\t%s = true\n\t\t}\n\n", cache.local, exportedName(f.sourceType), srcField, cache.loaded)

		if f.field != "" {
			fmt.Fprintf(w, "\t\tp.%s = %s.Value.%s\n", target.field, cache.local, exportedName(f.field))
		} else {
			find, ok, err := deriveCall(b.ix, target.acc, pdas[target.acc.Name], known, map[string]string{f.source: cache.local})
			if err != nil {
				return err
			}

			if !ok {
				return fmt.Errorf("%s: pda %s: cannot derive from %s", b.ix.Name, target.acc.Name, f.source)
			}

			derived := allocate(target.local, "Address")
			fmt.Fprintf(w, "\t\t%s, _, err := %s\n\t\tif err != nil {\n\t\t\treturn p, err\n\t\t}\n\n\t\tp.%s = %s\n", derived, find, target.field, derived)
		}

		w.WriteString("\t}\n\n")
	}

	w.WriteString("\treturn p, nil\n}\n\n")

	fmt.Fprintf(w, "func Build%[1]sInstructionOnChain(ctx context.Context, rpcClient *rpc.Client, opts core.Options, programID solana.PublicKey, p %[1]sParams) (solana.Instruction, error) {\n\tp, err := Resolve%[1]sParams(ctx, rpcClient, opts, programID, p)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n\n\treturn Build%[1]sInstruction(programID, p)\n}\n\n", name)

	return nil
}
