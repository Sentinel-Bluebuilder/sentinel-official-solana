package main

import (
	"bytes"
	"fmt"
)

const fetchPrelude = `import (
	"context"

	"%s/core"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/solana-foundation/solana-go/v2/rpc/ws"
)

`

func renderFetch(p *program) ([]byte, error) {
	layouts, err := accountLayouts(p.doc)
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer

	fmt.Fprintf(&b, "package %s\n", p.pkg)

	if len(layouts) == 0 {
		return b.Bytes(), nil
	}

	names := map[string]bool{}
	for _, l := range layouts {
		names[exportedName(l.name)] = true
	}

	for _, l := range layouts {
		if names[exportedName(l.name)+"Batch"] {
			return nil, fmt.Errorf("account %s: generated %s collides with another account", l.name, batchName(exportedName(l.name)))
		}
	}

	fmt.Fprintf(&b, "\n"+fetchPrelude, modulePath)

	for _, l := range layouts {
		name := exportedName(l.name)

		fmt.Fprintf(&b, `func Fetch%[1]s(ctx context.Context, rpcClient *rpc.Client, address solana.PublicKey, opts core.Options) (core.Keyed[%[1]s], error) {
	return core.Fetch(ctx, rpcClient, address, opts, Parse%[1]sAccount)
}

func %[3]s(ctx context.Context, rpcClient *rpc.Client, addresses []solana.PublicKey, opts core.Options) ([]core.Keyed[%[1]s], error) {
	return core.FetchBatch(ctx, rpcClient, addresses, opts, Parse%[1]sAccount)
}

func Find%[1]sAccounts(ctx context.Context, rpcClient *rpc.Client, programID solana.PublicKey, opts core.Options, filters ...rpc.RPCFilter) ([]core.Keyed[%[1]s], error) {
	return core.FindAccounts(ctx, rpcClient, programID, opts, append([]rpc.RPCFilter{%[1]sFilter()%[2]s}, filters...), Parse%[1]sAccount)
}

func Watch%[1]s(ctx context.Context, wsClient *ws.Client, address solana.PublicKey, opts core.Options) (<-chan core.Keyed[%[1]s], error) {
	return core.Watch(ctx, wsClient, address, opts, Parse%[1]sAccount)
}

`, name, sizeFilter(l), batchName(name))
	}

	return b.Bytes(), nil
}

func sizeFilter(l accountLayout) string {
	if !l.fixedSize {
		return ""
	}

	return ", {DataSize: " + exportedName(l.name) + "AccountSizeBytes}"
}
