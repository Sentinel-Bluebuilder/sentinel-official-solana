//go:build linux

package provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"gorm.io/gorm"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const requestTimeout = 15 * time.Second

func (r *Runtime) addRosterNodes(ctx context.Context, plan solana.PublicKey, nodes []solana.PublicKey) error {
	planAccount, err := r.programClient.FetchPlan(ctx, plan)
	if errors.Is(err, core.ErrAccountNotFound) || errors.Is(err, core.ErrOwnerMismatch) {
		return fmt.Errorf("%w: %w", daemonv1.ErrNotFound, err)
	}

	if err != nil {
		return fmt.Errorf("fetching plan %s: %w", plan, err)
	}

	chainPlan := planAccount.Value
	if chainPlan.Provider != r.address {
		return fmt.Errorf("%w: plan %s belongs to another provider", daemonv1.ErrFailedPrecondition, plan)
	}

	now := time.Now().Unix()

	cached := Plan{
		Address:         plan.String(),
		DataCapGB:       chainPlan.DataCapGB,
		DurationSeconds: chainPlan.DurationSeconds,
		MaxConnections:  chainPlan.MaxConnections,
		Price:           chainPlan.Price,
		Status:          chainPlan.Status,
		UpdatedUnix:     now,
	}
	if err := r.db.WithContext(ctx).Save(&cached).Error; err != nil {
		return fmt.Errorf("saving plan %s: %w", plan, err)
	}

	for _, node := range nodes {
		_, err := r.programClient.FetchNode(ctx, node)
		if errors.Is(err, core.ErrAccountNotFound) || errors.Is(err, core.ErrOwnerMismatch) {
			return fmt.Errorf("%w: %w", daemonv1.ErrNotFound, err)
		} else if err != nil {
			return fmt.Errorf("fetching node %s: %w", node, err)
		}

		row := RosterNode{
			AddedUnix: now,
			Node:      node.String(),
			Plan:      plan.String(),
		}
		if err := r.db.WithContext(ctx).Save(&row).Error; err != nil {
			return fmt.Errorf("saving roster node %s: %w", node, err)
		}
	}

	return nil
}

func (r *Runtime) fetchNodeMetadata(
	ctx context.Context, nodeAddress solana.PublicKey, node dvpn.Node,
) (nodeapi.Metadata, api.PeerURL, error) {
	return nodeapi.Discover(ctx, r.programClient, nodeAddress, node, requestTimeout, api.WithDialContext(r.dial))
}

func (r *Runtime) listRosterNodes(ctx context.Context, plan string) ([]RosterNode, error) {
	var rows []RosterNode

	query := r.db.WithContext(ctx)
	if plan != "" {
		query = query.Where("plan = ?", plan)
	}

	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}

	return rows, nil
}

func (r *Runtime) probeAndLease(ctx context.Context, node string) (Probe, string) {
	var probe Probe
	if err := r.db.WithContext(ctx).First(&probe, "node = ?", node).Error; err != nil &&
		!errors.Is(err, gorm.ErrRecordNotFound) {
		r.log.Warn().Err(err).Str("node", node).Msg("reading probe failed")
	}

	var lease Lease
	if err := r.db.WithContext(ctx).First(&lease, "node = ? AND healthy = ?", node, true).Error; err != nil {
		return probe, ""
	}

	return probe, lease.Address
}

func (r *Runtime) probeNodes(ctx context.Context) error {
	nodes, err := r.rosterNodeAddresses(ctx, "")
	if err != nil {
		return err
	}

	now := time.Now()

	for _, address := range nodes {
		probe := Probe{Node: address.String()}
		if err := r.db.WithContext(ctx).First(&probe, "node = ?", probe.Node).Error; err != nil &&
			!errors.Is(err, gorm.ErrRecordNotFound) {
			r.log.Warn().Err(err).Str("node", probe.Node).Msg("reading probe failed")
		}

		probe.Live = false
		probe.ProbedUnix = now.Unix()

		nodeAccount, fetchErr := r.programClient.FetchNode(ctx, address)
		if fetchErr == nil {
			node := nodeAccount.Value
			probe.Owner = node.Owner.String()

			metadata, uri, metadataErr := r.fetchNodeMetadata(ctx, address, node)
			if metadataErr == nil {
				probe.Live = true
				probe.TLSSHA256 = metadata.API.TLSSHA256
				probe.APIURL = uri.String()
				probe.HeartbeatSeconds = metadata.Settlement.HeartbeatSeconds
				probe.MinLeasePrepaySeconds = metadata.Settlement.MinLeasePrepaySeconds
			} else {
				r.log.Warn().Err(metadataErr).Str("node", probe.Node).Msg("probing node failed")
			}
		} else {
			r.log.Warn().Err(fetchErr).Str("node", probe.Node).Msg("probing node failed")
		}

		if probe.Live {
			probe.DeadSinceUnix = 0
			probe.LastLiveUnix = now.Unix()
		} else if probe.DeadSinceUnix == 0 {
			probe.DeadSinceUnix = now.Unix()
		}

		if err := r.db.WithContext(ctx).Save(&probe).Error; err != nil {
			return err
		}
	}

	return nil
}

func (r *Runtime) removeRosterNodes(ctx context.Context, plan solana.PublicKey, nodes []solana.PublicKey) error {
	for _, node := range nodes {
		if err := r.db.WithContext(ctx).Delete(
			&RosterNode{}, "plan = ? AND node = ?", plan.String(), node.String(),
		).Error; err != nil {
			return fmt.Errorf("removing roster node %s: %w", node, err)
		}
	}

	return nil
}

func (r *Runtime) rosterNodeAddresses(ctx context.Context, plan string) ([]solana.PublicKey, error) {
	rows, err := r.listRosterNodes(ctx, plan)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}

	addresses := make([]solana.PublicKey, 0, len(rows))
	for _, row := range rows {
		if seen[row.Node] {
			continue
		}

		seen[row.Node] = true

		address, err := solana.PublicKeyFromBase58(row.Node)
		if err == nil {
			addresses = append(addresses, address)
		}
	}

	return addresses, nil
}

func (r *Runtime) rosterNodeSet(ctx context.Context) (map[string]bool, error) {
	var rows []RosterNode
	if err := r.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}

	members := map[string]bool{}
	for _, row := range rows {
		members[row.Node] = true
	}

	return members, nil
}

func parsePublicKeys(raws []string) ([]solana.PublicKey, error) {
	keys := make([]solana.PublicKey, 0, len(raws))
	for _, raw := range raws {
		key, err := solana.PublicKeyFromBase58(raw)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", raw, err)
		}

		keys = append(keys, key)
	}

	return keys, nil
}
