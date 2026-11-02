//go:build e2e && integration

package e2e_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
	"github.com/sentinel-official/sentinel-go-sdk/v3/key"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/xray"
)

const (
	clientFundBaseUnits        = 20_000_000_000
	nodeCountry                = "DE"
	nodeOwnerFundBaseUnits     = 2 * sdktest.MinNodeBond
	ownerFundSOL               = 20
	providerOwnerFundBaseUnits = 2 * sdktest.MinProviderBond
	signerFundSOL              = 2
)

const (
	cliTimeout      = 30 * time.Second
	defaultServices = "wireguard"
	pollInterval    = 250 * time.Millisecond
	pollTimeout     = 90 * time.Second
	requireEnvVar   = "SENTINEL_DVPN_E2E_REQUIRE"
	servicesEnvVar  = "SENTINEL_DVPN_E2E_SERVICES"
)

var (
	errCLIFailed   = errors.New("cli command failed")
	errPollTimeout = errors.New("condition was never satisfied")
)

type localnetFixture struct {
	cli                            cliRunner
	cliBin                         string
	clientHome                     string
	clientInit                     initResult
	clientOwner                    solana.PrivateKey
	clientProc                     *exec.Cmd
	clientTag                      instance.Tag
	daemonBin                      string
	localnet                       *sdktest.Localnet
	nodeAddress                    string
	nodeHome                       string
	nodeInit                       initResult
	nodeLeasePricePerHourBaseUnits uint64
	nodeOwner                      solana.PrivateKey
	nodePricePerGBBaseUnits        uint64
	nodeTag                        instance.Tag
	planAddress                    string
	providerAddress                string
	providerHome                   string
	providerInit                   initResult
	providerOwner                  solana.PrivateKey
	providerProc                   *exec.Cmd
	providerTag                    instance.Tag
	service                        string
	subscriptionAddress            string
	topology                       *topology
}

func (lf *localnetFixture) closeNode(t *testing.T, ctx context.Context) {
	t.Helper()

	nodeCLI := lf.cli.withKey("node-owner")

	nodeCLI.run(t, ctx, "node", "close-node", "--node", lf.nodeAddress)
}

func (lf *localnetFixture) startDaemons(t *testing.T, ctx context.Context) {
	t.Helper()

	nodeCLI := lf.cli.withKey("node-owner")
	providerCLI := lf.cli.withKey("provider-owner")
	clientCLI := lf.cli.withKey("client-owner")

	rpcURL := bridgeURL(t, lf.localnet.RPCURL)
	wsURL := bridgeURL(t, lf.localnet.WSURL)

	nodeProc := startDaemon(
		t,
		ctx,
		lf.topology.node,
		lf.nodeHome,
		lf.daemonBin,
		"run",
		"--instance",
		string(lf.nodeTag),
		"--home",
		lf.nodeHome,
		"--rpc.url",
		rpcURL,
		"--rpc.ws-url",
		wsURL,
	)
	t.Cleanup(func() { stopDaemon(t, nodeProc, "node") })

	providerProc := startDaemon(
		t,
		ctx,
		lf.topology.provider,
		lf.providerHome,
		lf.daemonBin,
		"run",
		"--instance",
		string(lf.providerTag),
		"--home",
		lf.providerHome,
		"--rpc.url",
		rpcURL,
		"--rpc.ws-url",
		wsURL,
	)
	lf.providerProc = providerProc

	t.Cleanup(func() { stopDaemon(t, providerProc, "provider") })

	nodeDaemonCLI := nodeCLI.withDaemon(lf.nodeTag, lf.nodeHome)
	providerDaemonCLI := providerCLI.withDaemon(lf.providerTag, lf.providerHome)

	waitServing(t, ctx, nodeDaemonCLI)
	waitServing(t, ctx, providerDaemonCLI)

	waitForCondition(t, checkNodeURISynced(ctx, nodeCLI, lf.nodeAddress, lf.nodeInit.apiURL(hostOf(nodeCIDR))))

	providerDaemonCLI.run(t, ctx, "provider", "add-node", "--plan", lf.planAddress, lf.nodeAddress)
	providerDaemonCLI.run(t, ctx, "provider", "probe")
	providerDaemonCLI.run(t, ctx, "provider", "keep")
	waitForCondition(t, checkLeaseHealthy(ctx, providerDaemonCLI, lf.nodeAddress))

	clientProc := startDaemon(
		t,
		ctx,
		lf.topology.client,
		lf.clientHome,
		lf.daemonBin,
		"run",
		"--instance",
		string(lf.clientTag),
		"--home",
		lf.clientHome,
		"--rpc.url",
		rpcURL,
		"--rpc.ws-url",
		wsURL,
	)
	lf.clientProc = clientProc

	t.Cleanup(func() { stopDaemon(t, clientProc, "client") })

	clientDaemonCLI := clientCLI.withDaemon(lf.clientTag, lf.clientHome)
	waitServing(t, ctx, clientDaemonCLI)
}

func (lf *localnetFixture) startInfra(t *testing.T, ctx context.Context) {
	t.Helper()

	requirePrivilege(t)
	requireBinary(t, "nft")

	cliDir := cliSourceDir(t)

	lf.localnet = sdktest.StartLocalnet(t)

	lf.daemonBin, lf.cliBin = buildBinaries(t, ctx, cliDir, lf.localnet)
	verifyVersions(t, ctx, lf.localnet, lf.daemonBin, lf.cliBin)

	lf.topology = newTopology(t, ctx)
	installRPCNAT(t, ctx, urlPort(t, lf.localnet.RPCURL), urlPort(t, lf.localnet.WSURL))
	configureNodeNAT(t, ctx, lf.topology.node)
	startInternetServer(t, ctx, lf.topology)
	startDNSEchoServer(t, ctx, lf.topology)

	if lf.service == xray.Name.String() {
		startRealityDestServer(t, ctx, lf.topology)
	}
}

//nolint:paralleltest
func TestLocalnet_Sessions(t *testing.T) {
	for _, svc := range servicesToTest() {
		t.Run(svc, func(t *testing.T) {
			runLocalnet(t, svc)
		})
	}
}

func bridgeURL(t *testing.T, raw string) string {
	t.Helper()

	parsed, err := url.Parse(raw)
	require.NoError(t, err)

	parsed.Host = net.JoinHostPort(hostOf(bridgeCIDR), parsed.Port())

	return parsed.String()
}

func decodeJSON[T any](t *testing.T, raw []byte) T {
	t.Helper()

	var decoded T

	require.NoError(t, json.Unmarshal(raw, &decoded))

	return decoded
}

func failOrSkipf(t *testing.T, format string, args ...any) {
	t.Helper()

	msg := fmt.Sprintf(format, args...)
	if os.Getenv(requireEnvVar) == "1" {
		t.Fatal(msg)
	}

	t.Skip(msg)
}

func newInstanceTag(t *testing.T, role string) instance.Tag {
	t.Helper()

	var raw [4]byte

	_, err := rand.Read(raw[:])
	require.NoError(t, err)

	tag, err := instance.Parse(fmt.Sprintf("%s%d", role[:1], binary.BigEndian.Uint32(raw[:])%1_000_000_000))
	require.NoError(t, err)

	t.Cleanup(func() { removeToolCopies(t, tag) })

	return tag
}

func randomHexID(t *testing.T) string {
	t.Helper()

	var id [32]byte

	_, err := rand.Read(id[:])
	require.NoError(t, err)

	return hex.EncodeToString(id[:])
}

func randomPrivateKey(t *testing.T) solana.PrivateKey {
	t.Helper()

	privateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	return privateKey
}

func removeToolCopies(t *testing.T, tag instance.Tag) {
	t.Helper()

	var matches []string

	for _, dir := range service.ToolDirs() {
		found, err := filepath.Glob(dir + "/" + tag.Interface() + ".*")
		require.NoError(t, err)

		matches = append(matches, found...)
	}

	for _, match := range matches {
		if err := os.Remove(match); err != nil {
			t.Errorf("removing %s: %v", match, err)
		}
	}

	if len(matches) > 0 && !t.Failed() {
		t.Errorf("daemon %s left tool files: %v", tag, matches)
	}
}

func requireBinary(t *testing.T, name string) string {
	t.Helper()

	path, err := exec.LookPath(name)
	if err != nil {
		failOrSkipf(t, "%s not found on PATH", name)
	}

	return path
}

func requireDir(t *testing.T, dir string) {
	t.Helper()

	if _, err := os.Stat(dir); err != nil {
		failOrSkipf(t, "required directory not found: %s", dir)
	}
}

func requirePrivilege(t *testing.T) {
	t.Helper()

	if os.Geteuid() == 0 {
		return
	}

	failOrSkipf(t, "e2e suite requires root: daemons write the tools' shared folders under /etc and /usr/local/etc")
}

func runCLI(t *testing.T, ctx context.Context, bin string, args ...string) []byte {
	t.Helper()

	out, err := tryCLI(ctx, bin, args...)
	if err != nil {
		t.Fatalf("%s %s: %v", bin, strings.Join(args, " "), err)
	}

	return out
}

func runLocalnet(t *testing.T, svc string) {
	t.Helper()

	lf := &localnetFixture{service: svc}
	ctx := t.Context()
	lf.startInfra(t, ctx)
	lf.initRoles(t, ctx)
	lf.registerOnChain(t, ctx)
	lf.startDaemons(t, ctx)
	lf.runSessions(t, ctx)
	lf.closeNode(t, ctx)
}

func seedKeyFile(t *testing.T, home, name string, privateKey solana.PrivateKey) {
	t.Helper()

	path := filepath.Join(home, "keys", name+".json")
	require.NoError(t, key.Create(path, privateKey, ""))
}

func servicesToTest() []string {
	raw := os.Getenv(servicesEnvVar)
	if raw == "" {
		raw = defaultServices
	}

	return strings.Split(raw, ",")
}

func tryCLI(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, cliTimeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, args...)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), fmt.Errorf("%w: %w: %s", errCLIFailed, err, stderr.String())
	}

	return stdout.Bytes(), nil
}

func tryDecodeJSON[T any](raw []byte) (T, error) {
	var decoded T

	if err := json.Unmarshal(raw, &decoded); err != nil {
		return decoded, fmt.Errorf("decoding json: %w", err)
	}

	return decoded, nil
}

func urlPort(t *testing.T, raw string) int {
	t.Helper()

	parsed, err := url.Parse(raw)
	require.NoError(t, err)

	port, err := strconv.Atoi(parsed.Port())
	require.NoError(t, err)

	return port
}

func waitForCondition(t *testing.T, cond func() (bool, error)) {
	t.Helper()

	waitForConditionWithin(t, cond, pollTimeout)
}

func waitForConditionWithin(t *testing.T, cond func() (bool, error), timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)

	var lastErr error

	for time.Now().Before(deadline) {
		ok, err := cond()
		if ok {
			return
		}

		lastErr = err

		time.Sleep(pollInterval)
	}

	t.Fatalf("%v: %v", errPollTimeout, lastErr)
}
