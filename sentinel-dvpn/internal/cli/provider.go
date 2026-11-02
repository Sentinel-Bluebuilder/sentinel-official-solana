package cli

import (
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
)

func addProviderDaemonCmds(root *cobra.Command, daemon *dialer, printer *Printer) {
	ensureGroup(root, groupProvider).AddCommand(
		newProviderAddNodeCmd(daemon, printer),
		newProviderKeepCmd(daemon, printer),
		newProviderLeasesCmd(daemon, printer),
		newProviderNodesCmd(daemon, printer),
		newProviderProbeCmd(daemon, printer),
		newProviderRemoveNodeCmd(daemon, printer),
		newProviderStatusCmd(daemon, printer),
		newProviderTicketsCmd(daemon, printer),
	)
}

func newProviderAddNodeCmd(daemon *dialer, printer *Printer) *cobra.Command {
	toRequest := func(plan string, nodes []string) *daemonv1.AddRosterNodesRequest {
		return &daemonv1.AddRosterNodesRequest{
			Nodes: nodes,
			Plan:  plan,
		}
	}

	return newRosterEditCmd(
		daemon,
		printer,
		"add-node <address>...",
		"add nodes to the roster under a plan",
		"plan `address` the nodes are added under",
		daemonv1.ProviderServiceClient.AddRosterNodes,
		toRequest,
	)
}

func newProviderKeepCmd(daemon *dialer, printer *Printer) *cobra.Command {
	spec := callSpec[daemonv1.KeepNowResponse]{
		Args:  cobra.NoArgs,
		Role:  roleProvider,
		Short: "renew node keep-alive leases now",
		Use:   "keep",
	}

	return newUnaryCallCmd(
		daemon,
		printer,
		spec,
		daemonv1.NewProviderServiceClient,
		daemonv1.ProviderServiceClient.KeepNow,
		nil,
	)
}

func newProviderLeasesCmd(daemon *dialer, printer *Printer) *cobra.Command {
	spec := callSpec[daemonv1.ListLeasesResponse]{
		Args:  cobra.NoArgs,
		Role:  roleProvider,
		Short: "list leases the provider holds",
		Use:   "leases",
	}

	return newUnaryCallCmd(
		daemon,
		printer,
		spec,
		daemonv1.NewProviderServiceClient,
		daemonv1.ProviderServiceClient.ListLeases,
		nil,
	)
}

func newProviderNodesCmd(daemon *dialer, printer *Printer) *cobra.Command {
	spec := callSpec[daemonv1.ListRosterNodesResponse]{
		Args:  cobra.NoArgs,
		Role:  roleProvider,
		Short: "list nodes on the roster",
		Use:   "nodes",
	}

	return newUnaryCallCmd(
		daemon,
		printer,
		spec,
		daemonv1.NewProviderServiceClient,
		daemonv1.ProviderServiceClient.ListRosterNodes,
		nil,
	)
}

func newProviderProbeCmd(daemon *dialer, printer *Printer) *cobra.Command {
	spec := callSpec[daemonv1.ProbeNowResponse]{
		Args:  cobra.NoArgs,
		Role:  roleProvider,
		Short: "probe roster nodes now",
		Use:   "probe",
	}

	return newUnaryCallCmd(
		daemon,
		printer,
		spec,
		daemonv1.NewProviderServiceClient,
		daemonv1.ProviderServiceClient.ProbeNow,
		nil,
	)
}

func newProviderRemoveNodeCmd(daemon *dialer, printer *Printer) *cobra.Command {
	toRequest := func(plan string, nodes []string) *daemonv1.RemoveRosterNodesRequest {
		return &daemonv1.RemoveRosterNodesRequest{
			Nodes: nodes,
			Plan:  plan,
		}
	}

	return newRosterEditCmd(
		daemon,
		printer,
		"remove-node <address>...",
		"remove nodes from the roster under a plan",
		"plan `address` the nodes are removed from",
		daemonv1.ProviderServiceClient.RemoveRosterNodes,
		toRequest,
	)
}

func newProviderStatusCmd(daemon *dialer, printer *Printer) *cobra.Command {
	spec := callSpec[daemonv1.GetProviderInfoResponse]{
		Args:  cobra.NoArgs,
		Role:  roleProvider,
		Short: "print the provider's status",
		Use:   "status",
	}

	return newUnaryCallCmd(
		daemon,
		printer,
		spec,
		daemonv1.NewProviderServiceClient,
		daemonv1.ProviderServiceClient.GetProviderInfo,
		nil,
	)
}

func newProviderTicketsCmd(daemon *dialer, printer *Printer) *cobra.Command {
	spec := callSpec[daemonv1.ListIssuedTicketsResponse]{
		Args:  cobra.NoArgs,
		Role:  roleProvider,
		Short: "list tickets the provider has issued",
		Use:   "tickets",
	}

	return newUnaryCallCmd(
		daemon,
		printer,
		spec,
		daemonv1.NewProviderServiceClient,
		daemonv1.ProviderServiceClient.ListIssuedTickets,
		nil,
	)
}

func newRosterEditCmd[Req, Resp any](
	daemon *dialer,
	printer *Printer,
	use string,
	short string,
	planUsage string,
	call unaryCall[daemonv1.ProviderServiceClient, Req, Resp],
	toRequest func(plan string, nodes []string) *Req,
) *cobra.Command {
	var plan string

	spec := callSpec[Resp]{
		AddFlags: func(fs *pflag.FlagSet) {
			fs.StringVar(&plan, "plan", "", planUsage)
			_ = cobra.MarkFlagRequired(fs, "plan")
		},
		Args:  cobra.MinimumNArgs(1),
		Role:  roleProvider,
		Short: short,
		Use:   use,
	}
	newRequest := func(_ *cobra.Command, args []string) (*Req, error) { return toRequest(plan, args), nil }

	return newUnaryCallCmd(daemon, printer, spec, daemonv1.NewProviderServiceClient, call, newRequest)
}
