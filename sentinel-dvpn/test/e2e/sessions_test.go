//go:build e2e && integration

package e2e_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
)

const channelEscrowBaseUnits = 1_000_000_000

type addressView struct {
	Address string `json:"address"`
}

type channelView struct {
	Value struct {
		Redeemed uint64 `json:"redeemed"`
	} `json:"value"`
}

type nodeURIView struct {
	Value struct {
		URI string `json:"uri"`
	} `json:"value"`
}

func (lf *localnetFixture) runSessions(t *testing.T, ctx context.Context) {
	t.Helper()
	lf.runVoucherSession(t, ctx)
	lf.runStreamSession(t, ctx)
	lf.runTicketSession(t, ctx)
}

func (lf *localnetFixture) runStreamSession(t *testing.T, ctx context.Context) {
	t.Helper()

	clientCLI := lf.cli.withKey("client-owner")
	clientDaemonCLI := clientCLI.withDaemon(lf.clientTag, lf.clientHome)

	streamOut := clientDaemonCLI.run(
		t,
		ctx,
		"client",
		"start",
		"--node",
		lf.nodeAddress,
		"--new-channel",
		"--mode",
		"stream",
		"--escrow-base-units",
		strconv.Itoa(channelEscrowBaseUnits),
	)
	streamResp := decodeJSON[daemonv1.StartSessionResponse](t, streamOut)
	streamSessionID := streamResp.GetSession().GetId()
	require.NotEmpty(t, streamSessionID)

	waitForCondition(t, checkSessionActive(ctx, clientDaemonCLI, streamSessionID))
	clientDaemonCLI.run(t, ctx, "client", "stop", streamSessionID)
}

func (lf *localnetFixture) runTicketSession(t *testing.T, ctx context.Context) {
	t.Helper()

	session := lf.startTicketSession(t, ctx)
	clientCLI := lf.cli.withKey("client-owner").withDaemon(lf.clientTag, lf.clientHome)

	waitForCondition(t, checkSessionActive(ctx, clientCLI, session.GetId()))
	clientCLI.run(t, ctx, "client", "stop", session.GetId())
}

func (lf *localnetFixture) runVoucherSession(t *testing.T, ctx context.Context) {
	t.Helper()

	nodeCLI := lf.cli.withKey("node-owner")
	nodeDaemonCLI := nodeCLI.withDaemon(lf.nodeTag, lf.nodeHome)

	clientCLI := lf.cli.withKey("client-owner")
	clientDaemonCLI := clientCLI.withDaemon(lf.clientTag, lf.clientHome)

	startOut := clientDaemonCLI.run(
		t,
		ctx,
		"client",
		"start",
		"--node",
		lf.nodeAddress,
		"--new-channel",
		"--mode",
		"voucher",
		"--escrow-base-units",
		strconv.Itoa(channelEscrowBaseUnits),
	)
	startResp := decodeJSON[daemonv1.StartSessionResponse](t, startOut)

	sessionID := startResp.GetSession().GetId()
	channelAddress := startResp.GetSession().GetChannel()
	proxy := startResp.GetSession().GetProxyEndpoint()

	require.NotEmpty(t, sessionID)

	waitForCondition(t, checkSessionActive(ctx, clientDaemonCLI, sessionID))

	curlOut := lf.topology.client.run(t, ctx, "curl", curlArgs(proxy)...)
	require.Equal(t, httpProbeBody, string(curlOut))

	require.NoError(t, probeDNS(t, ctx, lf.topology.client, proxy, dnsProbeActiveTimeout))

	waitForCondition(t, checkSessionUsed(ctx, clientDaemonCLI, sessionID))

	nodeSessionsOut := nodeDaemonCLI.run(t, ctx, "node", "sessions")
	nodeSessions := decodeJSON[daemonv1.ListNodeSessionsResponse](t, nodeSessionsOut)
	require.NotEmpty(t, nodeSessions.GetSessions())

	dnsBefore := lf.topology.client.counter(t, ctx, dnsCounterName)
	internetBefore := lf.topology.client.counter(t, ctx, internetCounterName)
	require.Zero(t, dnsBefore)
	require.Zero(t, internetBefore)

	killSwitch := nftFamily + " " + lf.clientTag.ObjectName()
	require.Contains(t, lf.topology.client.tables(t, ctx), killSwitch)

	clientDaemonCLI.run(t, ctx, "client", "stop", sessionID)

	require.NotContains(t, lf.topology.client.tables(t, ctx), killSwitch)

	nodeDaemonCLI.run(t, ctx, "node", "redeem")
	waitForCondition(t, checkRedemptionConfirmed(ctx, nodeDaemonCLI))

	channelOut := nodeCLI.run(t, ctx, "channel", "show", channelAddress)
	channelInfo := decodeJSON[channelView](t, channelOut)
	require.Positive(t, channelInfo.Value.Redeemed)
}

func (lf *localnetFixture) startTicketSession(t *testing.T, ctx context.Context) *daemonv1.Session {
	t.Helper()

	clientCLI := lf.cli.withKey("client-owner")
	clientDaemonCLI := clientCLI.withDaemon(lf.clientTag, lf.clientHome)

	clientCLI.run(
		t,
		ctx,
		"subscription",
		"open-subscription",
		"--provider",
		lf.providerAddress,
		"--plan",
		lf.planAddress,
		"--owner",
		lf.clientOwner.PublicKey().String(),
		"--expected-price",
		strconv.Itoa(planPriceBaseUnits),
		"--expected-duration-seconds",
		strconv.Itoa(planDurationSeconds),
		"--expected-data-cap-gb",
		strconv.Itoa(planDataCapGB),
		"--expected-max-connections",
		strconv.Itoa(planMaxConnections),
	)
	lf.subscriptionAddress = searchAddress(
		t,
		ctx,
		clientCLI,
		"subscription",
		"owner",
		lf.clientOwner.PublicKey().String(),
		"",
	)

	ticketOut := clientDaemonCLI.run(
		t,
		ctx,
		"client",
		"start",
		"--node",
		lf.nodeAddress,
		"--ticket",
		"--provider",
		lf.providerAddress,
		"--subscription",
		lf.subscriptionAddress,
	)
	ticketResp := decodeJSON[daemonv1.StartSessionResponse](t, ticketOut)
	ticketSessionID := ticketResp.GetSession().GetId()
	require.NotEmpty(t, ticketSessionID)

	return ticketResp.GetSession()
}

func checkLeaseHealthy(ctx context.Context, cli cliRunner, nodeAddress string) func() (bool, error) {
	check := func() (bool, error) {
		out, err := cli.try(ctx, "provider", "leases")
		if err != nil {
			return false, err
		}

		resp, err := tryDecodeJSON[daemonv1.ListLeasesResponse](out)
		if err != nil {
			return false, err
		}

		for _, lease := range resp.GetLeases() {
			if lease.GetNode() == nodeAddress && lease.GetHealthy() {
				return true, nil
			}
		}

		return false, nil
	}

	return check
}

func checkNodeURISynced(ctx context.Context, cli cliRunner, nodeAddress, want string) func() (bool, error) {
	check := func() (bool, error) {
		out, err := cli.try(ctx, "node", "show", nodeAddress)
		if err != nil {
			return false, err
		}

		view, err := tryDecodeJSON[nodeURIView](out)
		if err != nil {
			return false, err
		}

		return view.Value.URI == want, nil
	}

	return check
}

func checkRedemptionConfirmed(ctx context.Context, cli cliRunner) func() (bool, error) {
	check := func() (bool, error) {
		out, err := cli.try(ctx, "node", "redemptions")
		if err != nil {
			return false, err
		}

		resp, err := tryDecodeJSON[daemonv1.ListRedemptionsResponse](out)
		if err != nil {
			return false, err
		}

		for _, redemption := range resp.GetRedemptions() {
			if redemption.GetStatus() == "confirmed" {
				return true, nil
			}
		}

		return false, nil
	}

	return check
}

func checkSessionActive(ctx context.Context, cli cliRunner, id string) func() (bool, error) {
	check := func() (bool, error) {
		out, err := cli.try(ctx, "client", "status", id)
		if err != nil {
			return false, err
		}

		resp, err := tryDecodeJSON[daemonv1.GetSessionResponse](out)
		if err != nil {
			return false, err
		}

		return resp.GetSession().GetStatus() == "active", nil
	}

	return check
}

func checkSessionUsed(ctx context.Context, cli cliRunner, id string) func() (bool, error) {
	check := func() (bool, error) {
		out, err := cli.try(ctx, "client", "status", id)
		if err != nil {
			return false, err
		}

		resp, err := tryDecodeJSON[daemonv1.GetSessionResponse](out)
		if err != nil {
			return false, err
		}

		usage := resp.GetSession().GetUsage()

		return usage.GetRxBytes()+usage.GetTxBytes() > 0, nil
	}

	return check
}

func searchAddress(
	t *testing.T,
	ctx context.Context,
	cli cliRunner,
	group,
	filterFlag,
	filterValue,
	status string,
) string {
	t.Helper()

	var address string

	check := func() (bool, error) {
		out, err := cli.try(ctx, searchArgs(group, filterFlag, filterValue, status)...)
		if err != nil {
			return false, err
		}

		results, err := tryDecodeJSON[[]addressView](out)
		if err != nil {
			return false, err
		}

		if len(results) == 0 {
			return false, nil
		}

		address = results[0].Address

		return true, nil
	}
	waitForCondition(t, check)

	return address
}

func searchArgs(group, filterFlag, filterValue, status string) []string {
	args := []string{group, "search", "--" + filterFlag, filterValue}
	if status != "" {
		args = append(args, "--status", status)
	}

	return args
}

func sessionStatus(t *testing.T, cli cliRunner, id string) *daemonv1.Session {
	t.Helper()

	out := cli.run(t, t.Context(), "client", "status", id)
	resp := decodeJSON[daemonv1.GetSessionResponse](t, out)

	return resp.GetSession()
}

func waitServing(t *testing.T, ctx context.Context, cli cliRunner) {
	t.Helper()

	check := func() (bool, error) {
		out, err := cli.try(ctx, "daemon", "status")
		if err != nil {
			return false, err
		}

		status, err := tryDecodeJSON[string](out)
		if err != nil {
			return false, err
		}

		return status == "SERVING", nil
	}
	waitForCondition(t, check)
}
