//go:build linux

package provider

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/solana-foundation/solana-go/v2"

	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

type grpcServer struct {
	daemonv1.UnimplementedProviderServiceServer

	runtime *Runtime
}

func (g *grpcServer) AddRosterNodes(
	ctx context.Context, req *daemonv1.AddRosterNodesRequest,
) (*daemonv1.AddRosterNodesResponse, error) {
	plan, nodes, err := parseRosterRequest(req.GetPlan(), req.GetNodes())
	if err != nil {
		return nil, daemonv1.ToStatus(err)
	}

	if err := g.runtime.addRosterNodes(ctx, plan, nodes); err != nil {
		return nil, daemonv1.ToStatus(err)
	}

	return &daemonv1.AddRosterNodesResponse{}, nil
}

func (g *grpcServer) GetProviderInfo(
	ctx context.Context, _ *daemonv1.GetProviderInfoRequest,
) (*daemonv1.GetProviderInfoResponse, error) {
	providerAccount, err := g.runtime.programClient.FetchProvider(ctx, g.runtime.address)
	if errors.Is(err, core.ErrAccountNotFound) || errors.Is(err, core.ErrOwnerMismatch) {
		return nil, daemonv1.ToStatus(fmt.Errorf("%w: provider %s: %w", daemonv1.ErrNotFound, g.runtime.address, err))
	}

	if err != nil {
		return nil, daemonv1.ToStatus(fmt.Errorf("fetching provider account %s: %w", g.runtime.address, err))
	}

	var nodeCount, leaseCount int64
	if err := g.runtime.db.WithContext(ctx).Model(&RosterNode{}).Distinct("node").Count(&nodeCount).Error; err != nil {
		return nil, daemonv1.ToStatus(err)
	}

	if err := g.runtime.db.WithContext(ctx).Model(&Lease{}).
		Where("status = ?", StatusOpen).Count(&leaseCount).Error; err != nil {
		return nil, daemonv1.ToStatus(err)
	}

	provider := providerAccount.Value

	return &daemonv1.GetProviderInfoResponse{Info: &daemonv1.ProviderInfo{
		Address:  g.runtime.address.String(),
		Id:       hex.EncodeToString(provider.ProviderID[:]),
		Leases:   uint32(leaseCount),
		Nodes:    uint32(nodeCount),
		Operator: g.runtime.signer.String(),
		Owner:    provider.Owner.String(),
		Status:   formatListingStatus(provider.Status),
	}}, nil
}

func (g *grpcServer) KeepNow(ctx context.Context, _ *daemonv1.KeepNowRequest) (*daemonv1.KeepNowResponse, error) {
	if err := g.runtime.keepLeases(ctx); err != nil {
		return nil, daemonv1.ToStatus(err)
	}

	return &daemonv1.KeepNowResponse{}, nil
}

func (g *grpcServer) ListIssuedTickets(
	ctx context.Context, _ *daemonv1.ListIssuedTicketsRequest,
) (*daemonv1.ListIssuedTicketsResponse, error) {
	var rows []Ticket
	if err := g.runtime.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, daemonv1.ToStatus(err)
	}

	tickets := make([]*daemonv1.IssuedTicket, 0, len(rows))
	for _, row := range rows {
		tickets = append(tickets, toProtoTicket(row))
	}

	return &daemonv1.ListIssuedTicketsResponse{Tickets: tickets}, nil
}

func (g *grpcServer) ListLeases(
	ctx context.Context, _ *daemonv1.ListLeasesRequest,
) (*daemonv1.ListLeasesResponse, error) {
	var rows []Lease
	if err := g.runtime.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, daemonv1.ToStatus(err)
	}

	leases := make([]*daemonv1.Lease, 0, len(rows))
	for _, row := range rows {
		leases = append(leases, toProtoLease(row))
	}

	return &daemonv1.ListLeasesResponse{Leases: leases}, nil
}

func (g *grpcServer) ListRosterNodes(
	ctx context.Context, _ *daemonv1.ListRosterNodesRequest,
) (*daemonv1.ListRosterNodesResponse, error) {
	rows, err := g.runtime.listRosterNodes(ctx, "")
	if err != nil {
		return nil, daemonv1.ToStatus(err)
	}

	nodes := make([]*daemonv1.RosterNode, 0, len(rows))
	for _, row := range rows {
		probe, lease := g.runtime.probeAndLease(ctx, row.Node)
		nodes = append(nodes, toProtoRosterNode(row, probe, lease))
	}

	return &daemonv1.ListRosterNodesResponse{Nodes: nodes}, nil
}

func (g *grpcServer) ProbeNow(ctx context.Context, _ *daemonv1.ProbeNowRequest) (*daemonv1.ProbeNowResponse, error) {
	if err := g.runtime.probeNodes(ctx); err != nil {
		return nil, daemonv1.ToStatus(err)
	}

	return &daemonv1.ProbeNowResponse{}, nil
}

func (g *grpcServer) RemoveRosterNodes(
	ctx context.Context, req *daemonv1.RemoveRosterNodesRequest,
) (*daemonv1.RemoveRosterNodesResponse, error) {
	plan, nodes, err := parseRosterRequest(req.GetPlan(), req.GetNodes())
	if err != nil {
		return nil, daemonv1.ToStatus(err)
	}

	if err := g.runtime.removeRosterNodes(ctx, plan, nodes); err != nil {
		return nil, daemonv1.ToStatus(err)
	}

	return &daemonv1.RemoveRosterNodesResponse{}, nil
}

func formatListingStatus(status uint8) string {
	if status == dvpn.ListingStatusListed {
		return "listed"
	}

	return "delisted"
}

func parseRosterRequest(rawPlan string, rawNodes []string) (solana.PublicKey, []solana.PublicKey, error) {
	plan, err := solana.PublicKeyFromBase58(rawPlan)
	if err != nil {
		return solana.PublicKey{}, nil, fmt.Errorf("%w: parsing plan %s: %w", daemonv1.ErrInvalidArgument, rawPlan, err)
	}

	nodes, err := parsePublicKeys(rawNodes)
	if err != nil {
		return solana.PublicKey{}, nil, fmt.Errorf("%w: %w", daemonv1.ErrInvalidArgument, err)
	}

	return plan, nodes, nil
}

func toProtoLease(row Lease) *daemonv1.Lease {
	return &daemonv1.Lease{
		Address:    row.Address,
		Cumulative: row.Cumulative,
		Escrow:     row.Escrow,
		Healthy:    row.Healthy,
		Mode:       uint32(row.Mode),
		Node:       row.Node,
		OpenedAt:   row.OpenedUnix,
		Status:     row.Status,
	}
}

func toProtoRosterNode(row RosterNode, probe Probe, lease string) *daemonv1.RosterNode {
	return &daemonv1.RosterNode{
		Lease:    lease,
		Live:     probe.Live,
		Node:     row.Node,
		Plan:     row.Plan,
		ProbedAt: probe.ProbedUnix,
	}
}

func toProtoTicket(row Ticket) *daemonv1.IssuedTicket {
	return &daemonv1.IssuedTicket{
		DataCapBytes: row.DataCapBytes,
		ExpiresAt:    row.ExpiresUnix,
		Hash:         row.Hash,
		IssuedAt:     row.IssuedUnix,
		Node:         row.Node,
		Subscription: row.Subscription,
		User:         row.User,
	}
}
