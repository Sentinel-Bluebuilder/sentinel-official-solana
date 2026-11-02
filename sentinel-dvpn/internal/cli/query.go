package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/spf13/cobra"

	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	progcli "github.com/sentinel-official/sentinel-programs/clients/go/cli"
	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

var ErrUnknownStatus = errors.New("unknown status")

//nolint:gochecknoglobals
var (
	channelFilters = []filterSpec{
		{dvpn.ChannelOwnerFilter, "owner"},
		{dvpn.ChannelNodeFilter, "node"},
	}
	grantFilters = []filterSpec{
		{dvpn.GrantGranterFilter, "granter"},
		{dvpn.GrantGranteeFilter, "grantee"},
		{dvpn.GrantTargetFilter, "target"},
	}
	leaseFilters = []filterSpec{
		{dvpn.LeaseProviderFilter, "provider"},
		{dvpn.LeaseNodeFilter, "node"},
	}
	nodeListing          = &listingSpec{dvpn.NodeStatusFilter, dvpn.ListingStatusDelisted, dvpn.ListingStatusListed}
	nodeUnbondingFilters = []filterSpec{{dvpn.NodeUnbondingOwnerFilter, "owner"}}
	pauseFeatures        = []pauseFeature{
		{dvpn.PauseNodeOpens, "node-opens"},
		{dvpn.PauseProviderOpens, "provider-opens"},
		{dvpn.PausePlanOpens, "plan-opens"},
		{dvpn.PauseSubscriptionOpens, "subscription-opens"},
		{dvpn.PauseLeaseOpens, "lease-opens"},
		{dvpn.PauseChannelOpens, "channel-opens"},
	}
	planFilters     = []filterSpec{{dvpn.PlanProviderFilter, "provider"}}
	planListing     = &listingSpec{dvpn.PlanStatusFilter, dvpn.ListingStatusDelisted, dvpn.ListingStatusListed}
	providerFilters = []filterSpec{{dvpn.ProviderOwnerFilter, "owner"}}
	providerListing = &listingSpec{
		dvpn.ProviderStatusFilter,
		dvpn.ListingStatusDelisted,
		dvpn.ListingStatusListed,
	}
	providerUnbondingFilters = []filterSpec{{dvpn.ProviderUnbondingOwnerFilter, "owner"}}
	subscriptionFilters      = []filterSpec{
		{dvpn.SubscriptionOwnerFilter, "owner"},
		{dvpn.SubscriptionPlanFilter, "plan"},
		{dvpn.SubscriptionProviderFilter, "provider"},
	}
	userFilters = []filterSpec{{dvpn.UserOwnerFilter, "owner"}}
)

type accountView[T any] struct {
	Address solana.PublicKey `json:"address"`
	Slot    uint64           `json:"slot"`
	Value   T                `json:"value"`
}

type fetchFunc[T any] func(context.Context, *rpc.Client, solana.PublicKey, core.Options) (core.Keyed[T], error)

type filterSpec struct {
	build func(solana.PublicKey) rpc.RPCFilter
	name  string
}

type findFunc[T any] func(
	context.Context,
	*rpc.Client,
	solana.PublicKey,
	core.Options,
	...rpc.RPCFilter,
) ([]core.Keyed[T], error)

type listingSpec struct {
	build    func(uint8) rpc.RPCFilter
	delisted uint8
	listed   uint8
}

func (l *listingSpec) parse(status string) (uint8, error) {
	switch status {
	case "delisted":
		return l.delisted, nil
	case "listed":
		return l.listed, nil
	default:
		return 0, fmt.Errorf("%w: %s", ErrUnknownStatus, status)
	}
}

type nodeSearchFlags struct {
	country                  string
	live                     bool
	maxPricePerGBBaseUnits   uint64
	maxPricePerHourBaseUnits uint64
	owner                    solana.PublicKey
	service                  string
	status                   string
}

type pausedResult struct {
	PausedFeatures []string `json:"paused_features"`
}

type pauseFeature struct {
	bit  uint8
	name string
}

func addQueryCmds(root *cobra.Command, programClient *dvpn.Client, printer *Printer) {
	ensureGroup(root, groupChannel).AddCommand(
		newShowCmd(programClient, printer, "channel", dvpn.FetchChannel),
		newSearchCmd(programClient, printer, dvpn.FindChannelAccounts, channelFilters, nil),
	)
	ensureGroup(root, groupGrant).AddCommand(
		newShowCmd(programClient, printer, "grant", dvpn.FetchGrant),
		newSearchCmd(programClient, printer, dvpn.FindGrantAccounts, grantFilters, nil),
	)
	ensureGroup(root, groupLease).AddCommand(
		newShowCmd(programClient, printer, "lease", dvpn.FetchLease),
		newSearchCmd(programClient, printer, dvpn.FindLeaseAccounts, leaseFilters, nil),
	)
	ensureGroup(root, groupNode).AddCommand(
		newShowCmd(programClient, printer, "node", dvpn.FetchNode),
		newNodeSearchCmd(programClient, printer),
	)
	ensureGroup(root, groupPlan).AddCommand(
		newShowCmd(programClient, printer, "plan", dvpn.FetchPlan),
		newSearchCmd(programClient, printer, dvpn.FindPlanAccounts, planFilters, planListing),
	)
	ensureGroup(root, groupProvider).AddCommand(
		newShowCmd(programClient, printer, "provider", dvpn.FetchProvider),
		newSearchCmd(programClient, printer, dvpn.FindProviderAccounts, providerFilters, providerListing),
	)
	ensureGroup(root, groupSubscription).AddCommand(
		newShowCmd(programClient, printer, "subscription", dvpn.FetchSubscription),
		newSearchCmd(programClient, printer, dvpn.FindSubscriptionAccounts, subscriptionFilters, nil),
	)
	ensureGroup(root, groupUser).AddCommand(
		newShowCmd(programClient, printer, "user", dvpn.FetchUser),
		newSearchCmd(programClient, printer, dvpn.FindUserAccounts, userFilters, nil),
	)
	showConfig := newShowCmd(programClient, printer, "config", dvpn.FetchConfig)
	showConfig.Short = "show program configuration"

	ensureGroup(root, groupProgram).AddCommand(
		showConfig,
		newProgramParamsCmd(programClient, printer),
		newProgramStatusCmd(programClient, printer),
	)
	ensureGroup(ensureGroup(root, groupNode), "unbonding").AddCommand(
		newShowCmd(programClient, printer, "unbonding", dvpn.FetchNodeUnbonding),
		newSearchCmd(programClient, printer, dvpn.FindNodeUnbondingAccounts, nodeUnbondingFilters, nil),
	)
	ensureGroup(ensureGroup(root, groupProvider), "unbonding").AddCommand(
		newShowCmd(programClient, printer, "unbonding", dvpn.FetchProviderUnbonding),
		newSearchCmd(programClient, printer, dvpn.FindProviderUnbondingAccounts, providerUnbondingFilters, nil),
	)
}

func buildListingFilter(listing *listingSpec, status string) (rpc.RPCFilter, bool, error) {
	if listing == nil || status == "" {
		return rpc.RPCFilter{}, false, nil
	}

	want, err := listing.parse(status)
	if err != nil {
		return rpc.RPCFilter{}, false, err
	}

	return listing.build(want), true, nil
}

func buildNodeFilters(flags nodeSearchFlags) ([]rpc.RPCFilter, error) {
	filters := []rpc.RPCFilter{}

	listing, ok, err := buildListingFilter(nodeListing, flags.status)
	if err != nil {
		return nil, err
	}

	if ok {
		filters = append(filters, listing)
	}

	if flags.country != "" {
		country, err := program.ParseCountry(flags.country)
		if err != nil {
			return nil, err
		}

		filters = append(filters, dvpn.NodeCountryFilter(country))
	}

	if !flags.owner.IsZero() {
		filters = append(filters, dvpn.NodeOwnerFilter(flags.owner))
	}

	return filters, nil
}

func newNodeSearchCmd(programClient *dvpn.Client, printer *Printer) *cobra.Command {
	var flags nodeSearchFlags

	run := func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()

		filters, err := buildNodeFilters(flags)
		if err != nil {
			return err
		}

		var chainCfg dvpn.Config

		if flags.live {
			account, err := programClient.FetchConfigBySeeds(ctx)
			if err != nil {
				return fmt.Errorf("fetching config: %w", err)
			}

			chainCfg = account.Value
		}

		predicate, err := nodePredicate(flags, chainCfg, time.Now().Unix())
		if err != nil {
			return err
		}

		accounts, err := dvpn.FindNodeAccounts(
			ctx,
			programClient.RPC,
			programClient.ProgramID,
			programClient.Options(),
			filters...,
		)
		if err != nil {
			return fmt.Errorf("searching nodes: %w", err)
		}

		matches := accounts[:0]
		for _, account := range accounts {
			if predicate(account.Value) {
				matches = append(matches, account)
			}
		}

		return printer.Print(toAccountViews(matches))
	}
	cmd := &cobra.Command{
		Args:  cobra.NoArgs,
		RunE:  run,
		Short: "search nodes",
		Use:   "search",
	}
	cmd.Flags().StringVar(&flags.country, "country", "", "two-letter country")
	cmd.Flags().BoolVar(&flags.live, "live", false, "only live nodes")
	cmd.Flags().Uint64Var(
		&flags.maxPricePerGBBaseUnits, "max-price-per-gb", 0, "maximum per-gb price `amount` in base units",
	)
	cmd.Flags().Uint64Var(
		&flags.maxPricePerHourBaseUnits, "max-price-per-hour", 0, "maximum per-hour price `amount` in base units",
	)
	progcli.Var(cmd.Flags(), &flags.owner, "owner", "node owner", "address", progcli.ParsePublicKey)
	cmd.Flags().StringVar(&flags.service, "service", "", "service the node must offer")
	cmd.Flags().StringVar(&flags.status, "status", "", "listed or delisted")

	return cmd
}

func newProgramParamsCmd(programClient *dvpn.Client, printer *Printer) *cobra.Command {
	run := func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()

		account, err := programClient.FetchConfigBySeeds(ctx)
		if err != nil {
			return fmt.Errorf("fetching config: %w", err)
		}

		return printer.Print(account.Value)
	}

	return &cobra.Command{
		Args:  cobra.NoArgs,
		RunE:  run,
		Short: "show program configuration",
		Use:   "params",
	}
}

func newProgramStatusCmd(programClient *dvpn.Client, printer *Printer) *cobra.Command {
	run := func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()

		account, err := programClient.FetchConfigBySeeds(ctx)
		if err != nil {
			return fmt.Errorf("fetching config: %w", err)
		}

		names := make([]string, 0, len(pauseFeatures))
		for _, feature := range pauseFeatures {
			if account.Value.PausedMask&feature.bit != 0 {
				names = append(names, feature.name)
			}
		}

		out := pausedResult{PausedFeatures: names}

		return printer.Print(out)
	}

	return &cobra.Command{
		Args:  cobra.NoArgs,
		RunE:  run,
		Short: "show paused program features",
		Use:   "status",
	}
}

func newSearchCmd[T any](
	programClient *dvpn.Client,
	printer *Printer,
	find findFunc[T],
	specs []filterSpec,
	listing *listingSpec,
) *cobra.Command {
	var status string

	values := make([]solana.PublicKey, len(specs))

	run := func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()

		filters := []rpc.RPCFilter{}

		for i, spec := range specs {
			if !values[i].IsZero() {
				filters = append(filters, spec.build(values[i]))
			}
		}

		listingFilter, ok, err := buildListingFilter(listing, status)
		if err != nil {
			return err
		}

		if ok {
			filters = append(filters, listingFilter)
		}

		found, err := find(ctx, programClient.RPC, programClient.ProgramID, programClient.Options(), filters...)
		if err != nil {
			return fmt.Errorf("searching accounts: %w", err)
		}

		return printer.Print(toAccountViews(found))
	}

	cmd := &cobra.Command{
		Args:  cobra.NoArgs,
		RunE:  run,
		Short: "search accounts",
		Use:   "search",
	}
	for i, spec := range specs {
		progcli.Var(cmd.Flags(), &values[i], spec.name, spec.name, "address", progcli.ParsePublicKey)
	}

	if listing != nil {
		cmd.Flags().StringVar(&status, "status", "", "listed or delisted")
	}

	return cmd
}

func newShowCmd[T any](programClient *dvpn.Client, printer *Printer, name string, fetch fetchFunc[T]) *cobra.Command {
	run := func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		address, err := parseAddress(name, args[0])
		if err != nil {
			return err
		}

		account, err := fetch(ctx, programClient.RPC, address, programClient.Options())
		if err != nil {
			return fmt.Errorf("fetching %s %s: %w", name, address, err)
		}

		out := accountView[T]{
			Address: account.Address,
			Slot:    account.Slot,
			Value:   account.Value,
		}

		return printer.Print(out)
	}

	cmd := &cobra.Command{
		Args:  cobra.ExactArgs(1),
		RunE:  run,
		Short: "show a " + name,
		Use:   "show <address>",
	}

	return cmd
}

func nodePredicate(flags nodeSearchFlags, chainCfg dvpn.Config, now int64) (func(dvpn.Node) bool, error) {
	var protocolBit uint32

	if flags.service != "" {
		name, err := service.ParseName(flags.service)
		if err != nil {
			return nil, fmt.Errorf("parsing service: %w", err)
		}

		protocolBit = name.Bit()
	}

	return func(node dvpn.Node) bool {
		if flags.live && !program.Live(node, chainCfg, now) {
			return false
		}

		if node.Protocols&protocolBit != protocolBit {
			return false
		}

		if flags.maxPricePerGBBaseUnits != 0 && node.ChannelPricePerGB > flags.maxPricePerGBBaseUnits {
			return false
		}

		if flags.maxPricePerHourBaseUnits != 0 && node.ChannelPricePerHour > flags.maxPricePerHourBaseUnits {
			return false
		}

		return true
	}, nil
}

func toAccountViews[T any](accounts []core.Keyed[T]) []accountView[T] {
	views := make([]accountView[T], 0, len(accounts))

	for _, account := range accounts {
		views = append(views, accountView[T]{
			Address: account.Address,
			Slot:    account.Slot,
			Value:   account.Value,
		})
	}

	return views
}
