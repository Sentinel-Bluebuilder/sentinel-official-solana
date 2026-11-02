package main

import (
	"fmt"
	"strings"

	"github.com/gagliardetto/anchor-go/idl"
	"github.com/gagliardetto/anchor-go/idl/idltype"
)

type resolution struct {
	dependencies []string
	field        string
	source       string
	sourceType   string
	target       int
}

func collectResolutions(p *program, b *builder, pdas map[string]pdaSpec) ([]resolution, error) {
	accounts := make([]idl.IdlInstructionAccount, 0, len(b.accounts))
	index := map[string]int{}
	for i, ba := range b.accounts {
		accounts = append(accounts, ba.acc)
		index[ba.acc.Name] = i
	}

	known := resolverKnown(b)
	var resolutions []resolution
	for i, ba := range b.accounts {
		if ba.kind != accountKindParam {
			continue
		}

		if len(ba.acc.Relations) > 0 {
			f, ok, err := resolveByRelation(p, b, accounts, index, i)
			if err != nil {
				return nil, err
			}

			if ok {
				resolutions = append(resolutions, f)

				continue
			}
		}

		if ba.acc.Pda.IsSome() {
			f, ok, err := resolveByPDA(p, b, accounts, index, known, pdas, i)
			if err != nil {
				return nil, err
			}

			if ok {
				resolutions = append(resolutions, f)
			}
		}

		if len(ba.acc.Relations) == 0 && ba.acc.Pda.IsNone() {
			if f, ok := resolveByPrefix(p, b, accounts, i); ok {
				resolutions = append(resolutions, f)
			} else if f, ok := rentRecipientResolution(p, b, accounts, i); ok {
				resolutions = append(resolutions, f)
			}
		}
	}

	return resolutions, nil
}

func declaredAccountType(doc *idl.Idl, ix *idl.IdlInstruction, accounts []idl.IdlInstructionAccount, name string) (string, error) {
	typ := ""
	for _, a := range accounts {
		if a.Pda.IsNone() {
			continue
		}

		for _, s := range a.Pda.Unwrap().Seeds {
			seed, ok := s.(*idl.IdlSeedAccount)
			if !ok || seed.Account.IsNone() || !strings.HasPrefix(seed.Path, name+".") {
				continue
			}

			declared := seed.Account.Unwrap()
			if declared == "" {
				return "", fmt.Errorf("%s: account %s: empty declared account type", ix.Name, name)
			}

			if typ != "" && typ != declared {
				return "", fmt.Errorf("%s: account %s: conflicting account types %s and %s", ix.Name, name, typ, declared)
			}

			typ = declared
		}
	}

	if typ == "" {
		for _, acc := range doc.Accounts {
			if exportedName(acc.Name) == exportedName(name) {
				if typ != "" && typ != acc.Name {
					return "", fmt.Errorf("%s: account %s: ambiguous account types %s and %s", ix.Name, name, typ, acc.Name)
				}

				typ = acc.Name
			}
		}
	}

	if typ == "" {
		return "", fmt.Errorf("%s: account %s: cannot determine its account type", ix.Name, name)
	}

	for _, acc := range doc.Accounts {
		if acc.Name == typ {
			return typ, nil
		}
	}

	return "", fmt.Errorf("%s: account %s: type %s is not an IDL account", ix.Name, name, typ)
}

func orderResolutions(b *builder, resolutions []resolution) ([]resolution, error) {
	targets := map[string]bool{}
	for _, f := range resolutions {
		targets[b.accounts[f.target].acc.Name] = true
	}

	done := map[string]bool{}
	out := make([]resolution, 0, len(resolutions))
	remaining := append([]resolution(nil), resolutions...)
	for len(remaining) > 0 {
		rest := remaining[:0]
		for _, f := range remaining {
			blocked := targets[f.source] && !done[f.source]
			for _, dependency := range f.dependencies {
				if targets[dependency] && !done[dependency] {
					blocked = true

					break
				}
			}

			if blocked {
				rest = append(rest, f)

				continue
			}

			out = append(out, f)
			done[b.accounts[f.target].acc.Name] = true
		}

		if len(rest) == len(remaining) {
			return nil, fmt.Errorf("%s: account %s: resolutions form a cycle", b.ix.Name, b.accounts[rest[0].target].acc.Name)
		}

		remaining = rest
	}

	return out, nil
}

// rentRecipientResolution fills the rent_recipient account from the
// rent_recipient field of the account the instruction closes. The IDL does not
// expose which account that is, so candidates are the writable state accounts
// with a rent_recipient field; several candidates are narrowed to the one
// whose name is part of the instruction name (close_node closes node).
// Anything still ambiguous stays unresolved.
func rentRecipientResolution(p *program, b *builder, accounts []idl.IdlInstructionAccount, i int) (resolution, bool) {
	if b.accounts[i].acc.Name != "rent_recipient" {
		return resolution{}, false
	}

	var candidates []resolution

	for j, src := range b.accounts {
		if j == i || src.kind != accountKindParam || !src.acc.Writable {
			continue
		}

		typ, err := declaredAccountType(p.doc, b.ix, accounts, src.acc.Name)
		if err != nil {
			continue
		}

		ft, err := fieldType(p.doc, typ, "rent_recipient")
		if err != nil {
			continue
		}

		if _, ok := ft.(*idltype.Pubkey); !ok {
			continue
		}

		candidates = append(candidates, resolution{
			target:       i,
			source:       src.acc.Name,
			sourceType:   typ,
			field:        "rent_recipient",
			dependencies: []string{src.acc.Name},
		})
	}

	if len(candidates) > 1 {
		named := candidates[:0:0]

		for _, c := range candidates {
			if strings.Contains("_"+b.ix.Name+"_", "_"+c.source+"_") {
				named = append(named, c)
			}
		}

		candidates = named
	}

	if len(candidates) != 1 {
		return resolution{}, false
	}

	return candidates[0], true
}

func resolveByPDA(p *program, b *builder, accounts []idl.IdlInstructionAccount, index map[string]int, known map[string]string, pdas map[string]pdaSpec, i int) (resolution, bool, error) {
	target := b.accounts[i]
	root := ""
	for _, s := range target.acc.Pda.Unwrap().Seeds {
		seed, ok := s.(*idl.IdlSeedAccount)
		if !ok || seed.Account.IsNone() {
			continue
		}

		name, _, _ := strings.Cut(seed.Path, ".")
		if name == target.acc.Name || (root != "" && root != name) {
			return resolution{}, false, nil
		}

		root = name
	}

	if root == "" {
		return resolution{}, false, nil
	}

	j, ok := index[root]
	if !ok || b.accounts[j].kind != accountKindParam {
		return resolution{}, false, nil
	}

	typ, err := declaredAccountType(p.doc, b.ix, accounts, root)
	if err != nil {
		return resolution{}, false, fmt.Errorf("%s: pda %s: %w", b.ix.Name, target.acc.Name, err)
	}

	spec, ok := pdas[target.acc.Name]
	if !ok {
		return resolution{}, false, fmt.Errorf("%s: pda %s: helper not found", b.ix.Name, target.acc.Name)
	}

	_, ok, err = deriveCall(b.ix, target.acc, spec, known, map[string]string{root: "source"})
	if err != nil || !ok {
		return resolution{}, false, err
	}

	dependencies := []string{root}
	seen := map[string]bool{root: true}
	for n, s := range target.acc.Pda.Unwrap().Seeds {
		seed, ok := s.(*idl.IdlSeedAccount)
		if !ok || seed.Account.IsSome() || spec.inlined[n] || seen[seed.Path] {
			continue
		}

		seen[seed.Path] = true
		dependencies = append(dependencies, seed.Path)
	}

	return resolution{
		target:       i,
		source:       root,
		sourceType:   typ,
		dependencies: dependencies,
	}, true, nil
}

func resolveByPrefix(p *program, b *builder, accounts []idl.IdlInstructionAccount, i int) (resolution, bool) {
	target := b.accounts[i]

	for j, src := range b.accounts {
		if j == i || src.kind != accountKindParam {
			continue
		}

		prefix := src.acc.Name + "_"
		if !strings.HasPrefix(target.acc.Name, prefix) {
			continue
		}

		field := strings.TrimPrefix(target.acc.Name, prefix)

		typ, err := declaredAccountType(p.doc, b.ix, accounts, src.acc.Name)
		if err != nil {
			continue
		}

		ft, err := fieldType(p.doc, typ, field)
		if err != nil {
			continue
		}

		if _, ok := ft.(*idltype.Pubkey); !ok {
			continue
		}

		return resolution{
			target:       i,
			source:       src.acc.Name,
			sourceType:   typ,
			field:        field,
			dependencies: []string{src.acc.Name},
		}, true
	}

	return resolution{}, false
}

func resolveByRelation(p *program, b *builder, accounts []idl.IdlInstructionAccount, index map[string]int, i int) (resolution, bool, error) {
	target := b.accounts[i]
	if len(target.acc.Relations) != 1 {
		return resolution{}, false, fmt.Errorf("%s: account %s: ambiguous multiple relations", b.ix.Name, target.acc.Name)
	}

	rel := target.acc.Relations[0]
	j, ok := index[rel]
	if !ok {
		return resolution{}, false, fmt.Errorf("%s: account %s: relation %s is not an instruction account", b.ix.Name, target.acc.Name, rel)
	}

	if b.accounts[j].kind != accountKindParam {
		return resolution{}, false, nil
	}

	typ, err := declaredAccountType(p.doc, b.ix, accounts, rel)
	if err != nil {
		return resolution{}, false, fmt.Errorf("%s: account %s: relation %s: %w", b.ix.Name, target.acc.Name, rel, err)
	}

	ft, err := fieldType(p.doc, typ, target.acc.Name)
	if err != nil {
		return resolution{}, false, fmt.Errorf("%s: account %s: relation %s: %w", b.ix.Name, target.acc.Name, rel, err)
	}

	if _, ok := ft.(*idltype.Pubkey); !ok {
		return resolution{}, false, fmt.Errorf("%s: account %s: relation %s: field %s is not a public key", b.ix.Name, target.acc.Name, rel, target.acc.Name)
	}

	return resolution{
		target:       i,
		source:       rel,
		sourceType:   typ,
		field:        target.acc.Name,
		dependencies: []string{rel},
	}, true, nil
}

func resolverKnown(b *builder) map[string]string {
	known := map[string]string{}
	for _, ba := range b.accounts {
		switch ba.kind {
		case accountKindParam, accountKindFixed, accountKindProgram:
			known[ba.acc.Name] = ba.expr
		}
	}

	return known
}
