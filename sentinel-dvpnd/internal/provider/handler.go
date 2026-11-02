//go:build linux

package provider

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/solana-foundation/solana-go/v2"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	"github.com/sentinel-official/sentinel-go-sdk/v3/providerapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/version"
)

type handler struct{ runtime *Runtime }

func (*handler) GetHealth(
	context.Context, providerapi.GetHealthRequestObject,
) (providerapi.GetHealthResponseObject, error) {
	return providerapi.GetHealth200JSONResponse(providerapi.Health{
		Status:  "ok",
		Version: version.Current().Tag,
	}), nil
}

func (h *handler) GetMetadata(
	context.Context, providerapi.GetMetadataRequestObject,
) (providerapi.GetMetadataResponseObject, error) {
	return providerapi.GetMetadata200JSONResponse(h.runtime.metadata.Current()), nil
}

func (h *handler) GetNodes(
	ctx context.Context, _ providerapi.GetNodesRequestObject,
) (providerapi.GetNodesResponseObject, error) {
	rosterNodes, err := h.runtime.rosterNodes(ctx, "")
	if err != nil {
		return nil, err
	}

	return providerapi.GetNodes200JSONResponse(providerapi.Nodes{Nodes: rosterNodes}), nil
}

func (h *handler) GetPlanNodes(
	ctx context.Context, req providerapi.GetPlanNodesRequestObject,
) (providerapi.GetPlanNodesResponseObject, error) {
	rosterNodes, err := h.runtime.rosterNodes(ctx, req.Plan.String())
	if err != nil {
		return nil, err
	}

	return providerapi.GetPlanNodes200JSONResponse(providerapi.Nodes{Nodes: rosterNodes}), nil
}

func (h *handler) IssueTicket(
	ctx context.Context, req providerapi.IssueTicketRequestObject,
) (providerapi.IssueTicketResponseObject, error) {
	if req.Body == nil {
		return nil, api.ErrBadRequest
	}

	result, err := h.runtime.issueTicket(ctx, req.Body.TicketRequest)
	if err != nil {
		return nil, err
	}

	return providerapi.IssueTicket200JSONResponse(result), nil
}

func (r *Runtime) buildMetadata(ctx context.Context) (providerapi.Metadata, error) {
	var nodeAddresses []string
	if err := r.db.WithContext(ctx).Model(&RosterNode{}).Distinct().Pluck("node", &nodeAddresses).Error; err != nil {
		return providerapi.Metadata{}, err
	}

	var plans []Plan
	if err := r.db.WithContext(ctx).Find(&plans).Error; err != nil {
		return providerapi.Metadata{}, err
	}

	nodes, err := parsePublicKeys(nodeAddresses)
	if err != nil {
		return providerapi.Metadata{}, err
	}

	planAddresses := make([]string, 0, len(plans))
	for _, plan := range plans {
		planAddresses = append(planAddresses, plan.Address)
	}

	planKeys, err := parsePublicKeys(planAddresses)
	if err != nil {
		return providerapi.Metadata{}, err
	}

	now := time.Now()

	apiURLs := make([]string, 0, len(r.cfg.API.Hosts))
	for _, host := range r.cfg.API.Hosts {
		apiURLs = append(apiURLs, apiURL(host, r.cfg.API.Port))
	}

	return providerapi.Metadata{
		API: providerapi.APIInfo{
			URLs:      apiURLs,
			TLSSHA256: r.apiServer.Fingerprint(),
		},
		ExpiresAt:  now.Add(auth.MetadataTTL).Unix(),
		IssuedAt:   now.Unix(),
		Nodes:      nodes,
		Operator:   r.signer,
		Owner:      r.owner,
		Plans:      planKeys,
		ProgramID:  r.programClient.ProgramID,
		Provider:   r.address,
		SDKVersion: version.SDK(),
		Version:    auth.MetadataVersion,
	}, nil
}

func (r *Runtime) refreshMetadata(ctx context.Context) error {
	return r.metadata.Refresh(ctx)
}

func (r *Runtime) rosterNodes(ctx context.Context, plan string) ([]providerapi.RosterNode, error) {
	nodes, err := r.rosterNodeAddresses(ctx, plan)
	if err != nil {
		return nil, err
	}

	rosterNodes := make([]providerapi.RosterNode, 0, len(nodes))
	for _, address := range nodes {
		probe, lease := r.probeAndLease(ctx, address.String())

		rosterNode := providerapi.RosterNode{
			Live:     probe.Live,
			Node:     address,
			ProbedAt: probe.ProbedUnix,
		}
		if leaseAddress, err := solana.PublicKeyFromBase58(lease); lease != "" && err == nil {
			rosterNode.Lease = &leaseAddress
		}

		rosterNodes = append(rosterNodes, rosterNode)
	}

	return rosterNodes, nil
}

func apiURL(host string, port uint16) string {
	return "https://" + net.JoinHostPort(host, strconv.FormatUint(uint64(port), 10))
}
