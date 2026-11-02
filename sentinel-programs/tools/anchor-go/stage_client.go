package main

import (
	"bytes"
	"fmt"
)

const clientPrelude = `import (
	"context"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/solana-foundation/solana-go/v2/rpc/ws"

	"%s/core"
)

type Client struct {
	Commitment rpc.CommitmentType
	ProgramID  solana.PublicKey
	RPC        *rpc.Client
}

func NewClient(rpcClient *rpc.Client, programID solana.PublicKey, commitment rpc.CommitmentType) *Client {
	return &Client{Commitment: commitment, ProgramID: programID, RPC: rpcClient}
}

func (c *Client) Options() core.Options {
	return core.Options{Commitment: c.Commitment, Owner: c.ProgramID}
}

`

func renderClient(p *program) ([]byte, error) {
	layouts, err := accountLayouts(p.doc)
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer

	fmt.Fprintf(&b, "package %s\n", p.pkg)

	if len(layouts) == 0 {
		return b.Bytes(), nil
	}

	for _, td := range p.doc.Types {
		if name := exportedName(td.Name); name == "Client" || name == "NewClient" {
			return nil, fmt.Errorf("type %s collides with the generated %s", td.Name, name)
		}
	}

	for _, constant := range p.doc.Constants {
		if name := exportedName(constant.Name); name == "Client" || name == "NewClient" {
			return nil, fmt.Errorf("constant %s collides with the generated %s", constant.Name, name)
		}
	}

	fmt.Fprintf(&b, "\n"+clientPrelude, modulePath)

	for _, l := range layouts {
		name := exportedName(l.name)

		fmt.Fprintf(&b, `func (c *Client) Fetch%[1]s(
	ctx context.Context,
	address solana.PublicKey,
) (core.Keyed[%[1]s], error) {
	return Fetch%[1]s(ctx, c.RPC, address, c.Options())
}

func (c *Client) %[2]s(ctx context.Context, addresses []solana.PublicKey) ([]core.Keyed[%[1]s], error) {
	return %[2]s(ctx, c.RPC, addresses, c.Options())
}

func (c *Client) Find%[1]sAccounts(ctx context.Context, filters ...rpc.RPCFilter) ([]core.Keyed[%[1]s], error) {
	return Find%[1]sAccounts(ctx, c.RPC, c.ProgramID, c.Options(), filters...)
}

func (c *Client) Watch%[1]s(
	ctx context.Context,
	wsClient *ws.Client,
	address solana.PublicKey,
) (<-chan core.Keyed[%[1]s], error) {
	return Watch%[1]s(ctx, wsClient, address, c.Options())
}

`, name, batchName(name))
	}

	return b.Bytes(), nil
}
