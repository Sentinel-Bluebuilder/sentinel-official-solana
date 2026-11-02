package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/spf13/cobra"

	"github.com/sentinel-official/sentinel-dvpn/internal/config"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
)

const requestTimeout = 15 * time.Second

var errEmptyResponseBody = errors.New("empty response body")

type nodeInspectResult struct {
	Health   *nodeapi.Health  `json:"health"`
	Metadata nodeapi.Metadata `json:"metadata"`
}

func addNodeDaemonCmds(root *cobra.Command, daemon *dialer, printer *Printer) {
	ensureGroup(root, groupNode).AddCommand(
		newNodeCloseSessionCmd(daemon, printer),
		newNodeHeartbeatCmd(daemon, printer),
		newNodePositionsCmd(daemon, printer),
		newNodeRedeemCmd(daemon, printer),
		newNodeRedemptionsCmd(daemon, printer),
		newNodeSessionCmd(daemon, printer),
		newNodeSessionsCmd(daemon, printer),
		newNodeStatusCmd(daemon, printer),
		newNodeSyncCmd(daemon, printer),
	)
}

func addNodeInspectCmd(
	root *cobra.Command,
	programClient *program.Client,
	signer *signerKey,
	cfg *config.File,
	printer *Printer,
) {
	ensureGroup(root, groupNode).AddCommand(newNodeInspectCmd(programClient, signer, cfg, printer))
}

func inspectNode(
	ctx context.Context,
	programClient *program.Client,
	signer *signerKey,
	nodeAddress solana.PublicKey,
	peer cli.PeerConfig,
) (nodeInspectResult, error) {
	node, err := programClient.FetchNode(ctx, nodeAddress)
	if err != nil {
		return nodeInspectResult{}, fmt.Errorf("fetching node %s: %w", nodeAddress, err)
	}

	dial := api.NewPeerDialer(net.DefaultResolver.LookupNetIP, peer.AllowPrivate)

	metadata, uri, err := nodeapi.Discover(
		ctx,
		programClient,
		nodeAddress,
		node.Value,
		requestTimeout,
		api.WithDialContext(dial),
	)
	if err != nil {
		return nodeInspectResult{}, err
	}

	privateKey, err := signer.PrivateKey(ctx)
	if err != nil {
		return nodeInspectResult{}, err
	}

	pinned, err := nodeapi.NewPinnedClient(
		uri.String(),
		metadata.API.TLSSHA256,
		privateKey,
		requestTimeout,
		api.WithDialContext(dial),
	)
	if err != nil {
		return nodeInspectResult{}, fmt.Errorf("dialing %s: %w", uri, err)
	}

	resp, err := pinned.GetHealthWithResponse(ctx)
	if err != nil {
		return nodeInspectResult{}, fmt.Errorf("fetching health from %s: %w", uri, err)
	}

	if resp.StatusCode() != http.StatusOK {
		return nodeInspectResult{}, fmt.Errorf(
			"fetching health from %s: %w",
			uri,
			api.ParseError(resp.StatusCode(), resp.Body),
		)
	}

	if resp.JSON200 == nil {
		return nodeInspectResult{}, fmt.Errorf("%w: %s: %s", errEmptyResponseBody, resp.Status(), resp.Body)
	}

	return nodeInspectResult{
		Health:   resp.JSON200,
		Metadata: metadata,
	}, nil
}

func newNodeCloseSessionCmd(daemon *dialer, printer *Printer) *cobra.Command {
	spec := callSpec[daemonv1.CloseNodeSessionResponse]{
		Args:  cobra.ExactArgs(1),
		Role:  roleNode,
		Short: "close a session the node is serving",
		Use:   "close-session <id>",
	}
	newRequest := func(_ *cobra.Command, args []string) (*daemonv1.CloseNodeSessionRequest, error) {
		return &daemonv1.CloseNodeSessionRequest{Id: args[0]}, nil
	}

	return newUnaryCallCmd(
		daemon,
		printer,
		spec,
		daemonv1.NewNodeServiceClient,
		daemonv1.NodeServiceClient.CloseNodeSession,
		newRequest,
	)
}

func newNodeHeartbeatCmd(daemon *dialer, printer *Printer) *cobra.Command {
	spec := callSpec[daemonv1.HeartbeatNowResponse]{
		Args:  cobra.NoArgs,
		Role:  roleNode,
		Short: "send a heartbeat now",
		Use:   "heartbeat",
	}

	return newUnaryCallCmd(
		daemon,
		printer,
		spec,
		daemonv1.NewNodeServiceClient,
		daemonv1.NodeServiceClient.HeartbeatNow,
		nil,
	)
}

func newNodeInspectCmd(
	programClient *program.Client,
	signer *signerKey,
	cfg *config.File,
	printer *Printer,
) *cobra.Command {
	run := func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		address, err := parseAddress("node", args[0])
		if err != nil {
			return err
		}

		out, err := inspectNode(ctx, programClient, signer, address, cfg.Peer)
		if err != nil {
			return err
		}

		return printer.Print(out)
	}

	return &cobra.Command{
		Args:  cobra.ExactArgs(1),
		RunE:  run,
		Short: "verify a node's signed metadata and check its health over a pinned connection",
		Use:   "inspect <address>",
	}
}

func newNodePositionsCmd(daemon *dialer, printer *Printer) *cobra.Command {
	spec := callSpec[daemonv1.ListPositionsResponse]{
		Args:  cobra.NoArgs,
		Role:  roleNode,
		Short: "list positions the node is watching",
		Use:   "positions",
	}

	return newUnaryCallCmd(
		daemon,
		printer,
		spec,
		daemonv1.NewNodeServiceClient,
		daemonv1.NodeServiceClient.ListPositions,
		nil,
	)
}

func newNodeRedeemCmd(daemon *dialer, printer *Printer) *cobra.Command {
	spec := callSpec[daemonv1.RedeemNowResponse]{
		Args:  cobra.NoArgs,
		Role:  roleNode,
		Short: "redeem outstanding positions now",
		Use:   "redeem",
	}

	return newUnaryCallCmd(daemon, printer, spec, daemonv1.NewNodeServiceClient, daemonv1.NodeServiceClient.RedeemNow, nil)
}

func newNodeRedemptionsCmd(daemon *dialer, printer *Printer) *cobra.Command {
	spec := callSpec[daemonv1.ListRedemptionsResponse]{
		Args:  cobra.NoArgs,
		Role:  roleNode,
		Short: "list past redemption attempts",
		Use:   "redemptions",
	}

	return newUnaryCallCmd(
		daemon,
		printer,
		spec,
		daemonv1.NewNodeServiceClient,
		daemonv1.NodeServiceClient.ListRedemptions,
		nil,
	)
}

func newNodeSessionCmd(daemon *dialer, printer *Printer) *cobra.Command {
	spec := callSpec[daemonv1.GetNodeSessionResponse]{
		Args:  cobra.ExactArgs(1),
		Role:  roleNode,
		Short: "print a session the node is serving",
		Use:   "session <id>",
	}
	newRequest := func(_ *cobra.Command, args []string) (*daemonv1.GetNodeSessionRequest, error) {
		return &daemonv1.GetNodeSessionRequest{Id: args[0]}, nil
	}

	return newUnaryCallCmd(
		daemon,
		printer,
		spec,
		daemonv1.NewNodeServiceClient,
		daemonv1.NodeServiceClient.GetNodeSession,
		newRequest,
	)
}

func newNodeSessionsCmd(daemon *dialer, printer *Printer) *cobra.Command {
	spec := callSpec[daemonv1.ListNodeSessionsResponse]{
		Args:  cobra.NoArgs,
		Role:  roleNode,
		Short: "list sessions the node is serving",
		Use:   "sessions",
	}

	return newUnaryCallCmd(
		daemon,
		printer,
		spec,
		daemonv1.NewNodeServiceClient,
		daemonv1.NodeServiceClient.ListNodeSessions,
		nil,
	)
}

func newNodeStatusCmd(daemon *dialer, printer *Printer) *cobra.Command {
	spec := callSpec[daemonv1.GetNodeInfoResponse]{
		Args:  cobra.NoArgs,
		Role:  roleNode,
		Short: "print the node's status",
		Use:   "status",
	}

	return newUnaryCallCmd(
		daemon,
		printer,
		spec,
		daemonv1.NewNodeServiceClient,
		daemonv1.NodeServiceClient.GetNodeInfo,
		nil,
	)
}

func newNodeSyncCmd(daemon *dialer, printer *Printer) *cobra.Command {
	spec := callSpec[daemonv1.SyncConfigNowResponse]{
		Args:  cobra.NoArgs,
		Role:  roleNode,
		Short: "push the daemon's loaded config to chain and refresh metadata (edit config.toml, then restart)",
		Use:   "sync",
	}

	return newUnaryCallCmd(
		daemon,
		printer,
		spec,
		daemonv1.NewNodeServiceClient,
		daemonv1.NodeServiceClient.SyncConfigNow,
		nil,
	)
}
