//go:build linux

package node

import (
	"context"
	"time"

	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/version"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func (r *Runtime) buildMetadata(ctx context.Context) (nodeapi.Metadata, error) {
	_, node := r.chainState()
	speed := r.bandwidth()
	now := time.Now()

	apiURLs := make([]string, 0, len(r.cfg.API.Hosts))
	for _, host := range r.cfg.API.Hosts {
		apiURLs = append(apiURLs, apiURL(host, r.cfg.API.Port))
	}

	active, err := r.countActiveSessions(ctx)
	if err != nil {
		return nodeapi.Metadata{}, err
	}

	metadata := nodeapi.Metadata{
		API: nodeapi.APIInfo{
			URLs:      apiURLs,
			TLSSHA256: r.apiServer.Fingerprint(),
		},
		Bandwidth:    speed,
		ExpiresAt:    now.Add(auth.MetadataTTL).Unix(),
		HandshakeDNS: r.cfg.HandshakeDNS.Enabled,
		IssuedAt:     now.Unix(),
		Location:     nodeapi.Location{Country: r.cfg.Country},
		Node:         r.address,
		Operator:     r.signer,
		Owner:        r.owner,
		Peers: nodeapi.Peers{
			Current: uint32(active),
			Max:     uint32(r.cfg.Sessions.MaxPeers),
		},
		ProgramID:  r.programClient.ProgramID,
		SDKVersion: version.SDK(),
		Service:    r.service.Metadata(),
		Settlement: r.buildSettlement(node),
		Version:    auth.MetadataVersion,
	}

	return metadata, nil
}

func (r *Runtime) buildSettlement(node dvpn.Node) nodeapi.Settlement {
	return nodeapi.Settlement{
		HeartbeatSeconds:        r.cfg.Sessions.HeartbeatSeconds,
		MinChannelPrepayBytes:   r.cfg.Sessions.MinPrepayBytes,
		MinChannelPrepaySeconds: r.cfg.Sessions.MinPrepaySeconds,
		MinLeasePrepaySeconds:   r.cfg.Lease.MinPrepaySeconds,
		Prices: nodeapi.Prices{
			ChannelPricePerGB:   node.ChannelPricePerGB,
			ChannelPricePerHour: node.ChannelPricePerHour,
			LeasePricePerHour:   node.LeasePricePerHour,
		},
	}
}

func (r *Runtime) refreshMetadata(ctx context.Context) error {
	if err := r.syncChain(ctx); err != nil {
		return err
	}

	return r.metadata.Refresh(ctx)
}

func (r *Runtime) signMetadata(metadata nodeapi.Metadata) (auth.SignedMessage, error) {
	return nodeapi.SignMetadata(r.privateKey, metadata)
}
