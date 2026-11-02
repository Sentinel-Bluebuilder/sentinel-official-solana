package program

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"

	"github.com/sentinel-official/sentinel-go-sdk/v3/version"
	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

var ErrMintMismatch = errors.New("config mint does not match the baked mint")

type Client struct {
	dvpn.Client

	Mint solana.PublicKey
}

func NewClient(rpcClient *rpc.Client, programID, mint solana.PublicKey, commitment rpc.CommitmentType) *Client {
	return &Client{
		Client: *dvpn.NewClient(rpcClient, programID, commitment),
		Mint:   mint,
	}
}

func (c *Client) AssociatedTokenAddress(owner solana.PublicKey) (solana.PublicKey, error) {
	address, _, err := solana.FindAssociatedTokenAddressWithProgram(owner, c.Mint, solana.Token2022ProgramID)
	if err != nil {
		return solana.PublicKey{}, fmt.Errorf("deriving token account: %w", err)
	}

	return address, nil
}

func (c *Client) CheckMint(ctx context.Context) error {
	cfg, err := c.Config(ctx)
	if err != nil {
		return err
	}

	if cfg.Mint != c.Mint {
		return fmt.Errorf("%w: config mint %s, baked mint %s", ErrMintMismatch, cfg.Mint, c.Mint)
	}

	return nil
}

func (c *Client) Config(ctx context.Context) (dvpn.Config, error) {
	account, err := c.FetchConfigBySeeds(ctx)
	if err != nil {
		return dvpn.Config{}, fmt.Errorf("fetching config: %w", err)
	}

	return account.Value, nil
}

func (c *Client) FindGrant(
	ctx context.Context,
	owner, signer solana.PublicKey,
	permission uint64,
	subjects ...solana.PublicKey,
) (*core.Keyed[dvpn.Grant], error) {
	addresses, err := c.grantCandidates(owner, signer, subjects)
	if err != nil {
		return nil, err
	}

	resp, err := c.RPC.GetMultipleAccountsWithOpts(ctx, addresses, &rpc.GetMultipleAccountsOpts{
		Commitment: c.Commitment,
		Encoding:   solana.EncodingBase64,
	})
	if err != nil {
		return nil, fmt.Errorf("fetching grants: %w", err)
	}

	if len(resp.Value) != len(addresses) {
		return nil, fmt.Errorf("expected %d grant accounts, got %d", len(addresses), len(resp.Value))
	}

	now := time.Now().Unix()

	for i, account := range resp.Value {
		if account == nil || !account.Owner.Equals(c.ProgramID) {
			continue
		}

		grant, err := dvpn.ParseGrantAccount(account.Data.GetBinary())
		if err != nil {
			return nil, fmt.Errorf("decoding grant %s: %w", addresses[i], err)
		}

		if grantCovers(*grant, owner, signer, permission, now) {
			return &core.Keyed[dvpn.Grant]{
				Address: addresses[i],
				Slot:    resp.Context.Slot,
				Value:   *grant,
			}, nil
		}
	}

	return nil, nil
}

func (c *Client) GrantAddress(
	ctx context.Context,
	owner, signer solana.PublicKey,
	permission uint64,
	subjects ...solana.PublicKey,
) (*solana.PublicKey, error) {
	if signer == owner {
		return nil, nil
	}

	grant, err := c.FindGrant(ctx, owner, signer, permission, subjects...)
	if err != nil || grant == nil {
		return nil, err
	}

	address := grant.Address

	return &address, nil
}

func (c *Client) HasGrant(
	ctx context.Context,
	owner, signer solana.PublicKey,
	permission uint64,
	subjects ...solana.PublicKey,
) (bool, error) {
	grant, err := c.FindGrant(ctx, owner, signer, permission, subjects...)
	if err != nil {
		return false, err
	}

	return grant != nil, nil
}

func (*Client) RemainingAccounts(subjects ...solana.PublicKey) []*solana.AccountMeta {
	out := make([]*solana.AccountMeta, 0, len(subjects))
	for _, subject := range subjects {
		out = append(out, solana.NewAccountMeta(subject, true, false))
	}

	return out
}

func (c *Client) VaultAddress() (solana.PublicKey, error) {
	config, _, err := dvpn.FindConfigAddress(c.ProgramID)
	if err != nil {
		return solana.PublicKey{}, fmt.Errorf("deriving config address: %w", err)
	}

	address, _, err := dvpn.FindVaultAddress(config, solana.Token2022ProgramID, c.Mint)
	if err != nil {
		return solana.PublicKey{}, fmt.Errorf("deriving vault address: %w", err)
	}

	return address, nil
}

func (c *Client) grantCandidates(
	owner, signer solana.PublicKey,
	subjects []solana.PublicKey,
) ([]solana.PublicKey, error) {
	targets := make([]solana.PublicKey, 0, len(subjects)+1)
	targets = append(targets, subjects...)
	targets = append(targets, solana.PublicKey{})

	out := make([]solana.PublicKey, 0, len(targets))
	for _, target := range targets {
		address, _, err := dvpn.FindGrantAddress(c.ProgramID, owner, signer, target)
		if err != nil {
			return nil, fmt.Errorf("deriving grant address: %w", err)
		}

		out = append(out, address)
	}

	return out, nil
}

func NewClientFromBuild(rpcClient *rpc.Client, commitment rpc.CommitmentType) (*Client, error) {
	programID, err := version.ParseProgramID()
	if err != nil {
		return nil, fmt.Errorf("parsing program id: %w", err)
	}

	mint, err := version.ParseMint()
	if err != nil {
		return nil, fmt.Errorf("parsing mint: %w", err)
	}

	return NewClient(rpcClient, programID, mint, commitment), nil
}

func grantCovers(grant dvpn.Grant, owner, signer solana.PublicKey, permission uint64, now int64) bool {
	return grant.Granter == owner && grant.Grantee == signer && grant.Permissions&permission == permission &&
		(grant.ExpiresAt == 0 || now < grant.ExpiresAt)
}
