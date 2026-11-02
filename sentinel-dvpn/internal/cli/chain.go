package cli

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/spf13/cobra"

	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const (
	groupChannel      = "channel"
	groupGrant        = "grant"
	groupLease        = "lease"
	groupNode         = "node"
	groupPlan         = "plan"
	groupProgram      = "program"
	groupProvider     = "provider"
	groupSubscription = "subscription"
	groupUser         = "user"
)

//nolint:gochecknoglobals
var (
	chainGroupByCommand = map[string]string{
		"accept-authority":          groupProgram,
		"close-channel":             groupChannel,
		"close-grant":               groupGrant,
		"close-lease":               groupLease,
		"close-node":                groupNode,
		"close-plan":                groupPlan,
		"close-provider":            groupProvider,
		"close-subscription":        groupSubscription,
		"delist-node":               groupNode,
		"delist-plan":               groupPlan,
		"delist-provider":           groupProvider,
		"fund-channels":             groupChannel,
		"fund-leases":               groupLease,
		initializeConfigCmdName:     groupProgram,
		"list-node":                 groupNode,
		"list-plan":                 groupPlan,
		"list-provider":             groupProvider,
		"open-channel":              groupChannel,
		"open-grant":                groupGrant,
		"open-lease":                groupLease,
		"open-node":                 groupNode,
		"open-plan":                 groupPlan,
		"open-provider":             groupProvider,
		"open-subscription":         groupSubscription,
		"propose-authority":         groupProgram,
		"redeem-channel-streams":    groupChannel,
		"redeem-channel-vouchers":   groupChannel,
		"redeem-lease-streams":      groupLease,
		"redeem-lease-vouchers":     groupLease,
		"set-params":                groupProgram,
		"set-paused-mask":           groupProgram,
		"set-treasury":              groupProgram,
		"start-close-channel":       groupChannel,
		"start-close-lease":         groupLease,
		"sweep-config-fees":         groupProgram,
		"sweep-node-fees":           groupNode,
		"sweep-provider-fees":       groupProvider,
		"update-grant":              groupGrant,
		"update-node":               groupNode,
		"update-plan":               groupPlan,
		"update-provider":           groupProvider,
		"withdraw-node-bond":        groupNode,
		"withdraw-node-revenue":     groupNode,
		"withdraw-provider-balance": groupProvider,
		"withdraw-provider-bond":    groupProvider,
		"withdraw-provider-revenue": groupProvider,
		"withdraw-user-balance":     groupUser,
	}

	errTargetAmbiguous = errors.New("--target and --any-target are mutually exclusive")
	preRunByCommand    = map[string]func(*runner, *cobra.Command) error{
		"close-channel":         func(r *runner, cmd *cobra.Command) error { return r.resolvePositionNode(cmd, "channel") },
		"close-lease":           func(r *runner, cmd *cobra.Command) error { return r.resolvePositionNode(cmd, "lease") },
		"open-grant":            setOpenGrantWildcards,
		"withdraw-user-balance": (*runner).resolveUserRentRecipient,
	}
)

func (r *runner) resolvePositionNode(cmd *cobra.Command, kind string) error {
	if cmd.Flags().Changed("node") || !cmd.Flags().Changed(kind) {
		return nil
	}

	address, err := parseAddress(kind, cmd.Flags().Lookup(kind).Value.String())
	if err != nil {
		return err
	}

	var node solana.PublicKey

	if kind == "channel" {
		channel, err := dvpn.FetchChannel(cmd.Context(), r.RPC(), address, r.Options())
		if err != nil {
			return err
		}

		node = channel.Value.Node
	} else {
		lease, err := dvpn.FetchLease(cmd.Context(), r.RPC(), address, r.Options())
		if err != nil {
			return err
		}

		node = lease.Value.Node
	}

	return setChainFlag(cmd, "node", node)
}

func (r *runner) resolveUserRentRecipient(cmd *cobra.Command) error {
	if cmd.Flags().Changed("rent-recipient") {
		return nil
	}

	address, _, err := dvpn.FindUserAddress(r.ProgramID(), r.resolved.owner)
	if err != nil {
		return err
	}

	user, err := dvpn.FetchUser(cmd.Context(), r.RPC(), address, r.Options())
	if err != nil {
		return err
	}

	return setChainFlag(cmd, "rent-recipient", user.Value.RentRecipient)
}

func ensureGroup(parent *cobra.Command, name string) *cobra.Command {
	for _, cmd := range parent.Commands() {
		if cmd.Name() == name {
			return cmd
		}
	}

	cmd := &cobra.Command{Use: name}
	parent.AddCommand(cmd)

	return cmd
}

func newChainGroups(r *runner) []*cobra.Command {
	names := []string{
		groupChannel,
		groupGrant,
		groupLease,
		groupNode,
		groupPlan,
		groupProgram,
		groupProvider,
		groupSubscription,
		groupUser,
	}
	groups := make([]*cobra.Command, 0, len(names))
	byName := make(map[string]*cobra.Command, len(names))

	for _, name := range names {
		group := &cobra.Command{Use: name}
		groups = append(groups, group)
		byName[name] = group
	}

	for _, cmd := range dvpn.Commands(r) {
		group, ok := chainGroupByCommand[cmd.Name()]
		if !ok {
			continue
		}

		if cmd.Name() == "open-grant" {
			cmd.Flags().Bool("any-target", false, "allow any target")
			cmd.Flags().Bool("never-expires", false, "never expire")
			cmd.Flags().Bool("unlimited", false, "allow unlimited use")
		}

		cmd.PreRunE = r.prepareWith(cmd.Name())
		markSending(cmd)
		byName[group].AddCommand(cmd)
	}

	return groups
}

func setChainFlag(cmd *cobra.Command, name string, address solana.PublicKey) error {
	if err := cmd.Flags().Set(name, address.String()); err != nil {
		return fmt.Errorf("setting --%s: %w", name, err)
	}

	return nil
}

func setOpenGrantWildcards(_ *runner, cmd *cobra.Command) error {
	anyTarget, err := cmd.Flags().GetBool("any-target")
	if err != nil {
		return fmt.Errorf("reading --%s: %w", "any-target", err)
	}

	if anyTarget && cmd.Flags().Changed("target") {
		return errTargetAmbiguous
	}

	for _, option := range []struct {
		name     string
		value    string
		wildcard string
	}{
		{
			name:     "target",
			value:    (solana.PublicKey{}).String(),
			wildcard: "any-target",
		},
		{
			name:     "expires-at",
			value:    "0",
			wildcard: "never-expires",
		},
		{
			name:     "allowance",
			value:    strconv.FormatUint(dvpn.AllowanceUnlimited, 10),
			wildcard: "unlimited",
		},
	} {
		enabled, err := cmd.Flags().GetBool(option.wildcard)
		if err != nil {
			return fmt.Errorf("reading --%s: %w", option.wildcard, err)
		}

		if !enabled || cmd.Flags().Changed(option.name) {
			continue
		}

		if err := cmd.Flags().Set(option.name, option.value); err != nil {
			return fmt.Errorf("setting --%s: %w", option.name, err)
		}
	}

	return nil
}
