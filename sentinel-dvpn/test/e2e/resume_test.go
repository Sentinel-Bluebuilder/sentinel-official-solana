//go:build e2e && integration

package e2e_test

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
)

const (
	resumeEscrowBaseUnits     = 2_000_000_000
	resumePrepayBytes         = 100_000_000
	resumePricePerGBBaseUnits = 1_000_000_000
)

//nolint:paralleltest
func TestResume_Localnet(t *testing.T) {
	for _, svc := range servicesToTest() {
		t.Run(svc, func(t *testing.T) { runResume(t, svc) })
	}
}

func checkRedemptionSignaturesConfirmed(ctx context.Context, cli cliRunner, signatures []string) func() (bool, error) {
	return func() (bool, error) {
		out, err := cli.try(ctx, "node", "redemptions")
		if err != nil {
			return false, err
		}

		resp, err := tryDecodeJSON[daemonv1.ListRedemptionsResponse](out)
		if err != nil {
			return false, err
		}

		confirmed := make(map[string]bool)

		for _, redemption := range resp.GetRedemptions() {
			if !slices.Contains(signatures, redemption.GetSignature()) {
				continue
			}

			if redemption.GetStatus() == "failed" {
				return false, fmt.Errorf("redemption %s failed: %s", redemption.GetSignature(), redemption.GetError())
			}

			confirmed[redemption.GetSignature()] = redemption.GetStatus() == "confirmed"
		}

		for _, signature := range signatures {
			if !confirmed[signature] {
				return false, nil
			}
		}

		return true, nil
	}
}

func checkSessionEnded(ctx context.Context, cli cliRunner, id string) func() (bool, error) {
	return func() (bool, error) {
		out, err := cli.try(ctx, "client", "status", id)
		if err != nil {
			return false, err
		}

		resp, err := tryDecodeJSON[daemonv1.GetSessionResponse](out)
		if err != nil {
			return false, err
		}

		return resp.GetSession().GetStatus() == "ended", nil
	}
}

func probeSession(t *testing.T, lf *localnetFixture, cli cliRunner, session *daemonv1.Session) {
	t.Helper()

	ctx := t.Context()
	out := lf.topology.client.run(t, ctx, "curl", curlArgs(session.GetProxyEndpoint())...)
	require.Equal(t, httpProbeBody, string(out))
	waitForCondition(t, checkSessionUsed(ctx, cli, session.GetId()))
}

func redeemChannelAndWait(
	t *testing.T,
	ctx context.Context,
	lf *localnetFixture,
	cli cliRunner,
	address string,
) uint64 {
	t.Helper()

	beforeOut := cli.run(t, ctx, "node", "redemptions")
	before := decodeJSON[daemonv1.ListRedemptionsResponse](t, beforeOut)
	statusesBefore := make(map[string]string)

	for _, redemption := range before.GetRedemptions() {
		statusesBefore[redemption.GetSignature()] = redemption.GetStatus()
	}

	cli.run(t, ctx, "node", "redeem")
	afterOut := cli.run(t, ctx, "node", "redemptions")
	after := decodeJSON[daemonv1.ListRedemptionsResponse](t, afterOut)

	var signatures []string

	for _, redemption := range after.GetRedemptions() {
		previousStatus := statusesBefore[redemption.GetSignature()]
		if !slices.Contains(redemption.GetSubjects(), address) || (previousStatus != "" && previousStatus != "pending") {
			continue
		}

		signatures = append(signatures, redemption.GetSignature())
	}

	if len(signatures) > 0 {
		waitForCondition(t, checkRedemptionSignaturesConfirmed(ctx, cli, signatures))
	}

	channelOut := lf.cli.run(t, ctx, "channel", "show", address)
	redeemedBaseUnits := decodeJSON[channelView](t, channelOut).Value.Redeemed
	positionsOut := cli.run(t, ctx, "node", "positions")
	positions := decodeJSON[daemonv1.ListPositionsResponse](t, positionsOut)

	for _, position := range positions.GetPositions() {
		if position.GetAddress() == address {
			require.GreaterOrEqual(t, redeemedBaseUnits, position.GetCumulative(),
				"channel must have no outstanding voucher after redemption (tracked signatures: %v)", signatures)

			return redeemedBaseUnits
		}
	}

	t.Fatalf("channel %s not found in node positions", address)

	return 0
}

func restartClientDaemon(t *testing.T, ctx context.Context, lf *localnetFixture, cli cliRunner) {
	t.Helper()

	logPath := filepath.Join(lf.clientHome, "daemon-resume.log")
	proc := startDaemonWithLog(t, ctx, lf.topology.client, logPath, lf.daemonBin,
		"run", "--instance", string(lf.clientTag), "--home", lf.clientHome,
		"--rpc.url", bridgeURL(t, lf.localnet.RPCURL),
		"--rpc.ws-url", bridgeURL(t, lf.localnet.WSURL))
	lf.clientProc = proc

	t.Cleanup(func() { stopDaemon(t, proc, "client") })
	waitServing(t, ctx, cli)
}

func runResume(t *testing.T, svc string) {
	t.Helper()

	lf := &localnetFixture{service: svc}
	ctx := t.Context()
	lf.startInfra(t, ctx)
	lf.initRoles(t, ctx)

	setInstanceConfig(t, lf.nodeHome, int64(resumePricePerGBBaseUnits), "prices", "channel_per_gb")
	setInstanceConfig(t, lf.nodeHome, 1, "redeem", "threshold_base_units")
	lf.nodePricePerGBBaseUnits = resumePricePerGBBaseUnits
	setInstanceConfig(t, lf.clientHome, int64(resumePrepayBytes), "prepay_bytes")
	lf.registerOnChain(t, ctx)
	lf.startDaemons(t, ctx)

	nodeCLI := lf.cli.withKey("node-owner").withDaemon(lf.nodeTag, lf.nodeHome)
	clientCLI := lf.cli.withKey("client-owner").withDaemon(lf.clientTag, lf.clientHome)
	startOut := clientCLI.run(t, ctx, "client", "start", "--node", lf.nodeAddress,
		"--new-channel", "--mode", "voucher", "--escrow-base-units", strconv.Itoa(resumeEscrowBaseUnits))
	start := decodeJSON[daemonv1.StartSessionResponse](t, startOut)
	first := start.GetSession()
	require.NotEmpty(t, first.GetId())
	require.NotEmpty(t, first.GetChannel())
	waitForCondition(t, checkSessionActive(ctx, clientCLI, first.GetId()))

	probeSession(t, lf, clientCLI, first)
	nodeCLI.run(t, ctx, "node", "redeem")
	waitForCondition(t, checkRedemptionConfirmed(ctx, nodeCLI))
	channelOut := lf.cli.run(t, ctx, "channel", "show", first.GetChannel())
	firstRedeemedBaseUnits := decodeJSON[channelView](t, channelOut).Value.Redeemed
	require.Positive(t, firstRedeemedBaseUnits)

	require.NoError(t, lf.clientProc.Process.Signal(syscall.SIGKILL))
	require.False(t, stopProcess(lf.clientProc, stopTimeout))
	require.Equal(t, syscall.SIGKILL, lf.clientProc.ProcessState.Sys().(syscall.WaitStatus).Signal())
	restartClientDaemon(t, ctx, lf, clientCLI)

	resumeOut := clientCLI.run(t, ctx, "client", "start", "--node", lf.nodeAddress,
		"--channel", first.GetChannel())
	resume := decodeJSON[daemonv1.StartSessionResponse](t, resumeOut)
	second := resume.GetSession()
	require.NotEmpty(t, second.GetId())
	require.NotEqual(t, first.GetId(), second.GetId())
	require.Equal(t, first.GetChannel(), second.GetChannel())
	waitForCondition(t, checkSessionActive(ctx, clientCLI, second.GetId()))

	nodeOut := nodeCLI.run(t, ctx, "node", "sessions")
	nodeSessions := decodeJSON[daemonv1.ListNodeSessionsResponse](t, nodeOut)
	activeCount := 0

	for _, session := range nodeSessions.GetSessions() {
		if session.GetSubject() == first.GetChannel() && session.GetStatus() == "active" {
			activeCount++
		}
	}

	require.Equal(t, 1, activeCount, "resuming must replace the crashed session on the node")
	probeSession(t, lf, clientCLI, second)
	clientCLI.run(t, ctx, "client", "stop", second.GetId())
	require.Less(t, sessionStatus(t, clientCLI, second.GetId()).GetSpent(), uint64(resumePrepayBytes))
	redeemedBaseUnits := redeemChannelAndWait(t, ctx, lf, nodeCLI, first.GetChannel())
	require.GreaterOrEqual(t, redeemedBaseUnits, firstRedeemedBaseUnits)
}
