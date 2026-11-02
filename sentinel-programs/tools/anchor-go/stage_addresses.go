package main

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/gagliardetto/solana-go"
)

type fixedAddress struct {
	name      string
	publicKey solana.PublicKey
}

func collectAddresses(p *program) ([]fixedAddress, map[string]string, error) {
	byName := map[string]solana.PublicKey{}
	byKey := map[solana.PublicKey]string{}
	idents := map[string]string{}

	for i := range p.doc.Instructions {
		ix := &p.doc.Instructions[i]

		accounts, err := flattenAccounts(ix.Accounts)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", ix.Name, err)
		}

		for _, a := range accounts {
			if a.Address.IsNone() {
				continue
			}

			key := a.Address.Unwrap()

			if prev, ok := byName[a.Name]; ok && !prev.Equals(key) {
				return nil, nil, fmt.Errorf("%s: account %s has address %s but %s elsewhere", ix.Name, a.Name, key, prev)
			}

			byName[a.Name] = key

			if p.idlAddress != nil && key.Equals(*p.idlAddress) {
				continue
			}

			if _, ok := byKey[key]; !ok {
				byKey[key] = exportedName(a.Name) + "Address"
			}

			idents[a.Name] = byKey[key]
		}
	}

	out := make([]fixedAddress, 0, len(byKey))
	for key, name := range byKey {
		out = append(out, fixedAddress{
			name:      name,
			publicKey: key,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })

	return out, idents, nil
}

func renderAddresses(p *program) ([]byte, error) {
	addresses, _, err := collectAddresses(p)
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer

	fmt.Fprintf(&b, "package %s\n", p.pkg)

	if len(addresses) == 0 {
		return b.Bytes(), nil
	}

	b.WriteString("\nimport \"github.com/solana-foundation/solana-go/v2\"\n\nvar (\n")

	for _, a := range addresses {
		fmt.Fprintf(&b, "\t%s = solana.MustPublicKeyFromBase58(%q)\n", a.name, a.publicKey.String())
	}

	b.WriteString(")\n")

	return b.Bytes(), nil
}
