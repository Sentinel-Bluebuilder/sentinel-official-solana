package main

import (
	"bytes"
	"fmt"
	"strings"
)

func renderFetchPDAs(p *program) ([]byte, error) {
	layouts, err := accountLayouts(p.doc)
	if err != nil {
		return nil, err
	}

	specs, _, err := collectPDAs(p.doc)
	if err != nil {
		return nil, err
	}

	accounts := map[string]string{}
	fetchNames := map[string]bool{}
	for _, l := range layouts {
		name := exportedName(l.name)
		accounts[name] = name
		fetchNames["Fetch"+name] = true
		fetchNames[batchName(name)] = true
	}

	for _, td := range p.doc.Types {
		fetchNames[exportedName(td.Name)] = true
	}
	for _, constant := range p.doc.Constants {
		fetchNames[exportedName(constant.Name)] = true
	}

	var body bytes.Buffer

	for _, s := range specs {
		name, ok := accounts[exportedName(s.typeName)]
		if !ok || s.program != "" {
			continue
		}

		if symbol := "Fetch" + name + "BySeeds"; fetchNames[symbol] {
			return nil, fmt.Errorf("account %s: generated %s collides with another declaration", s.name, symbol)
		}

		writeFetchPDA(&body, name, s)
	}

	var b bytes.Buffer

	fmt.Fprintf(&b, "package %s\n", p.pkg)

	if body.Len() != 0 {
		fmt.Fprintf(&b, `
import (
	"context"
	"fmt"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"

	"%s/core"
)

`, modulePath)
		b.Write(body.Bytes())
	}

	return b.Bytes(), nil
}

func writeFetchPDA(b *bytes.Buffer, name string, s pdaSpec) {
	params := []string{}
	args := []string{}
	used := map[string]bool{
		"address": true, "c": true, "ctx": true, "err": true,
		"opts": true, "programID": true, "rpcClient": true, "wsClient": true,
		"context": true, "core": true, "fmt": true, "rpc": true, "solana": true,
	}

	for _, prm := range s.params {
		param := prm.name
		for used[param] {
			param += "Seed"
		}

		used[param] = true
		params = append(params, param+" "+prm.goType)
		args = append(args, param)
	}

	paramSuffix := ""
	argSuffix := ""
	if len(params) != 0 {
		paramSuffix = ",\n\t" + strings.Join(params, ",\n\t")
		argSuffix = ", " + strings.Join(args, ", ")
	}

	fmt.Fprintf(b, `func Fetch%[1]sBySeeds(
	ctx context.Context,
	rpcClient *rpc.Client,
	programID solana.PublicKey,
	opts core.Options%[2]s,
) (core.Keyed[%[1]s], error) {
	address, _, err := Find%[1]sAddress(programID%[3]s)
	if err != nil {
		return core.Keyed[%[1]s]{}, fmt.Errorf("deriving %[4]s address: %%w", err)
	}

	return Fetch%[1]s(ctx, rpcClient, address, opts)
}

func (c *Client) Fetch%[1]sBySeeds(
	ctx context.Context%[2]s,
) (core.Keyed[%[1]s], error) {
	return Fetch%[1]sBySeeds(ctx, c.RPC, c.ProgramID, c.Options()%[3]s)
}

`, name, paramSuffix, argSuffix, s.name)
}
