//go:build e2e && integration

package e2e_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/programs/system"
	"github.com/stretchr/testify/require"

	_ "github.com/sentinel-official/sentinel-go-sdk/v3/amneziawg"
	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
	_ "github.com/sentinel-official/sentinel-go-sdk/v3/hysteria2"
	_ "github.com/sentinel-official/sentinel-go-sdk/v3/openvpn"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
	_ "github.com/sentinel-official/sentinel-go-sdk/v3/v2ray"
	_ "github.com/sentinel-official/sentinel-go-sdk/v3/wireguard"
	"github.com/sentinel-official/sentinel-go-sdk/v3/xray"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const (
	configFileName      = "config.toml"
	lamportsPerSOL      = 1_000_000_000
	planDataCapGB       = 1_000
	planDurationSeconds = 86_400
	planMaxConnections  = 10
	planPriceBaseUnits  = 1
)

type initResult struct {
	APIPort   uint16 `json:"api_port,omitempty"`
	ID        string `json:"id,omitempty"`
	PublicKey string `json:"public_key"`
}

func (r initResult) apiURL(host string) string {
	port := strconv.FormatUint(uint64(r.APIPort), 10)

	return "https://" + net.JoinHostPort(host, port)
}

func (lf *localnetFixture) fundOwners(t *testing.T, ctx context.Context) {
	t.Helper()

	lf.nodeOwner = randomPrivateKey(t)
	lf.providerOwner = randomPrivateKey(t)
	lf.clientOwner = randomPrivateKey(t)

	require.NoError(t, lf.localnet.FundSOL(ctx, lf.nodeOwner.PublicKey(), ownerFundSOL))
	require.NoError(t, lf.localnet.FundSOL(ctx, lf.providerOwner.PublicKey(), ownerFundSOL))
	require.NoError(t, lf.localnet.FundSOL(ctx, lf.clientOwner.PublicKey(), ownerFundSOL))
	require.NoError(t, lf.localnet.FundTokens(ctx, lf.clientOwner.PublicKey(), clientFundBaseUnits))
	require.NoError(t, lf.localnet.FundTokens(ctx, lf.nodeOwner.PublicKey(), nodeOwnerFundBaseUnits))
	require.NoError(t, lf.localnet.FundTokens(ctx, lf.providerOwner.PublicKey(), providerOwnerFundBaseUnits))
}

func (lf *localnetFixture) initHomes(t *testing.T, ctx context.Context) {
	t.Helper()

	work := t.TempDir()
	cliHome := filepath.Join(work, "sentinel-dvpn")

	seedKeyFile(t, cliHome, "node-owner", lf.nodeOwner)
	seedKeyFile(t, cliHome, "provider-owner", lf.providerOwner)
	seedKeyFile(t, cliHome, "client-owner", lf.clientOwner)

	lf.cli = cliRunner{
		bin:    lf.cliBin,
		home:   cliHome,
		rpcURL: lf.localnet.RPCURL,
		wsURL:  lf.localnet.WSURL,
	}

	lf.clientTag = newInstanceTag(t, "client")
	lf.nodeTag = newInstanceTag(t, "node")
	lf.providerTag = newInstanceTag(t, "provider")

	lf.nodeHome = filepath.Join(work, string(lf.nodeTag))
	lf.providerHome = filepath.Join(work, string(lf.providerTag))
	lf.clientHome = filepath.Join(work, string(lf.clientTag))

	lf.nodeInit = runInit(
		t,
		ctx,
		lf.daemonBin,
		lf.nodeTag,
		lf.nodeHome,
		lf.nodeOwner.PublicKey().String(),
		lf.service,
		nodeCountry,
		hostOf(nodeCIDR),
	)
	lf.providerInit = runInit(
		t,
		ctx,
		lf.daemonBin,
		lf.providerTag,
		lf.providerHome,
		lf.providerOwner.PublicKey().String(),
		"",
		"",
		hostOf(providerCIDR),
	)
	require.NotZero(t, lf.nodeInit.APIPort)

	if lf.service == xray.Name.String() {
		setXrayRealityDest(t, lf.nodeHome)
	}

	lf.nodePricePerGBBaseUnits = setNodePriceFromRedeemThreshold(t, lf.nodeHome)
	setInstanceConfig(t, lf.nodeHome, 1, "redeem", "threshold_base_units")
	setInstanceConfig(t, lf.nodeHome, false, "speedtest", "enabled")

	lf.clientInit = runInit(
		t,
		ctx,
		lf.daemonBin,
		lf.clientTag,
		lf.clientHome,
		lf.clientOwner.PublicKey().String(),
		"",
		"",
		"",
	)
	setInstanceConfig(t, lf.clientHome, "none", "network", "dns_backend")
}

func (lf *localnetFixture) initRoles(t *testing.T, ctx context.Context) {
	t.Helper()
	lf.fundOwners(t, ctx)
	lf.initHomes(t, ctx)
}

func (lf *localnetFixture) registerClient(t *testing.T, ctx context.Context) {
	t.Helper()

	nodeOperatorAddress, err := solana.PublicKeyFromBase58(lf.nodeInit.PublicKey)
	require.NoError(t, err)

	providerOperatorAddress, err := solana.PublicKeyFromBase58(lf.providerInit.PublicKey)
	require.NoError(t, err)

	fundSOLDirect(t, ctx, lf.localnet, lf.nodeOwner, nodeOperatorAddress, signerFundSOL)
	fundSOLDirect(t, ctx, lf.localnet, lf.providerOwner, providerOperatorAddress, signerFundSOL)

	clientCLI := lf.cli.withKey("client-owner")
	clientCLI.run(
		t,
		ctx,
		"grant",
		"open-grant",
		"--grantee",
		lf.clientInit.PublicKey,
		"--target",
		solana.PublicKey{}.String(),
		"--permissions",
		strconv.FormatUint(program.PermsClient, 10),
		"--expires-at",
		"0",
		"--allowance",
		strconv.FormatUint(dvpn.AllowanceUnlimited, 10),
	)

	clientOperatorAddress, err := solana.PublicKeyFromBase58(lf.clientInit.PublicKey)
	require.NoError(t, err)

	fundSOLDirect(t, ctx, lf.localnet, lf.clientOwner, clientOperatorAddress, signerFundSOL)

	clientCLI.run(t, ctx, "wallet", "approve-delegate", "--amount-base-units", "unlimited")
}

func (lf *localnetFixture) registerNode(t *testing.T, ctx context.Context) {
	t.Helper()

	nodeCLI := lf.cli.withKey("node-owner")
	protocolBit := protocolBitFor(t, lf.service)

	nodeOwnerAddress := lf.nodeOwner.PublicKey().String()
	protocolArg := strconv.FormatUint(uint64(protocolBit), 10)
	nodeCLI.run(
		t,
		ctx,
		"node",
		"open-node",
		"--node-id",
		lf.nodeInit.ID,
		"--bond",
		strconv.FormatUint(sdktest.MinNodeBond, 10),
		"--uri",
		"https://node.example.com",
		"--country",
		nodeCountry,
		"--protocols",
		protocolArg,
		"--prices.channel-price-per-gb",
		strconv.FormatUint(lf.nodePricePerGBBaseUnits, 10),
		"--prices.channel-price-per-hour",
		"0",
		"--prices.lease-price-per-hour",
		strconv.FormatUint(lf.nodeLeasePricePerHourBaseUnits, 10),
	)
	nodeCLI.run(
		t,
		ctx,
		"grant",
		"open-grant",
		"--grantee",
		lf.nodeInit.PublicKey,
		"--target",
		solana.PublicKey{}.String(),
		"--permissions",
		strconv.FormatUint(program.PermsNode, 10),
		"--expires-at",
		"0",
		"--allowance",
		"0",
	)

	lf.nodeAddress = searchAddress(t, ctx, nodeCLI, "node", "owner", nodeOwnerAddress, "")
	nodeCLI.run(t, ctx, "node", "list-node", "--node", lf.nodeAddress)
	require.Equal(t, lf.nodeAddress, searchAddress(t, ctx, nodeCLI, "node", "owner", nodeOwnerAddress, "listed"))
}

func (lf *localnetFixture) registerOnChain(t *testing.T, ctx context.Context) {
	t.Helper()
	lf.registerNode(t, ctx)
	lf.registerProvider(t, ctx)
	lf.registerPlan(t, ctx)
	lf.registerClient(t, ctx)
}

func (lf *localnetFixture) registerPlan(t *testing.T, ctx context.Context) {
	t.Helper()

	providerCLI := lf.cli.withKey("provider-owner")

	planID := randomHexID(t)
	providerCLI.run(
		t,
		ctx,
		"plan",
		"open-plan",
		"--plan-id",
		planID,
		"--provider",
		lf.providerAddress,
		"--price",
		strconv.Itoa(planPriceBaseUnits),
		"--duration-seconds",
		strconv.Itoa(planDurationSeconds),
		"--data-cap-gb",
		strconv.Itoa(planDataCapGB),
		"--max-connections",
		strconv.Itoa(planMaxConnections),
	)

	lf.planAddress = searchAddress(t, ctx, providerCLI, "plan", "provider", lf.providerAddress, "")
	providerCLI.run(t, ctx, "plan", "list-plan", "--plan", lf.planAddress, "--provider", lf.providerAddress)
	require.Equal(t, lf.planAddress, searchAddress(t, ctx, providerCLI, "plan", "provider", lf.providerAddress, "listed"))
}

func (lf *localnetFixture) registerProvider(t *testing.T, ctx context.Context) {
	t.Helper()

	providerCLI := lf.cli.withKey("provider-owner")
	providerOwnerAddress := lf.providerOwner.PublicKey().String()

	providerCLI.run(
		t,
		ctx,
		"provider",
		"open-provider",
		"--provider-id",
		lf.providerInit.ID,
		"--bond",
		strconv.FormatUint(sdktest.MinProviderBond, 10),
		"--uri",
		"https://provider.example.com",
	)
	providerCLI.run(
		t,
		ctx,
		"grant",
		"open-grant",
		"--grantee",
		lf.providerInit.PublicKey,
		"--target",
		solana.PublicKey{}.String(),
		"--permissions",
		strconv.FormatUint(program.PermsProvider, 10),
		"--expires-at",
		"0",
		"--allowance",
		strconv.FormatUint(dvpn.AllowanceUnlimited, 10),
	)
	providerCLI.run(t, ctx, "wallet", "approve-delegate", "--amount-base-units", "unlimited")

	lf.providerAddress = searchAddress(t, ctx, providerCLI, "provider", "owner", providerOwnerAddress, "")
	providerCLI.run(t, ctx, "provider", "list-provider", "--provider", lf.providerAddress)
	require.Equal(
		t,
		lf.providerAddress,
		searchAddress(t, ctx, providerCLI, "provider", "owner", providerOwnerAddress, "listed"),
	)
}

func fundSOLDirect(
	t *testing.T,
	ctx context.Context,
	localnet *sdktest.Localnet,
	from solana.PrivateKey,
	to solana.PublicKey,
	amountSOL uint64,
) {
	t.Helper()

	ix := system.NewTransferInstruction(amountSOL*lamportsPerSOL, from.PublicKey(), to).Build()
	ixs := []solana.Instruction{ix}

	_, err := tx.Send(ctx, localnet.RPC, localnet.WS, from, tx.DefaultConfig(), ixs)
	require.NoError(t, err)
}

func initArgs(tag instance.Tag, home, owner, serviceType, country, host string) []string {
	args := []string{
		"init",
		"--instance",
		string(tag),
		"--home",
		home,
		"--owner",
		owner,
		"--plain-key",
		"--peer-allow-private",
	}

	if serviceType != "" {
		args = append(args, "--service", serviceType)
	}

	if country != "" {
		args = append(args, "--country", country)
	}

	if host != "" {
		args = append(args, "--api-hosts", host)
	}

	return args
}

func instanceConfig(t *testing.T, home string) map[string]any {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(home, configFileName)) //nolint:gosec
	require.NoError(t, err)

	var doc map[string]any

	require.NoError(t, toml.Unmarshal(raw, &doc))

	return doc
}

func protocolBitFor(t *testing.T, svc string) uint32 {
	t.Helper()

	name, err := service.ParseName(svc)
	require.NoError(t, err)

	return name.Bit()
}

func runInit(
	t *testing.T,
	ctx context.Context,
	daemonBin string,
	tag instance.Tag,
	home,
	owner,
	serviceType,
	country,
	host string,
) initResult {
	t.Helper()

	out := runCLI(t, ctx, daemonBin, initArgs(tag, home, owner, serviceType, country, host)...)

	return decodeJSON[initResult](t, out)
}

func setInstanceConfig(t *testing.T, home string, value any, path ...string) {
	t.Helper()
	require.NotEmpty(t, path)

	doc := instanceConfig(t, home)
	table := doc

	for _, key := range path[:len(path)-1] {
		next, ok := table[key].(map[string]any)
		if !ok {
			next = make(map[string]any)
			table[key] = next
		}

		table = next
	}

	table[path[len(path)-1]] = value
	raw, err := toml.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, configFileName), raw, 0o600))
}

func setNodePriceFromRedeemThreshold(t *testing.T, home string) uint64 {
	t.Helper()
	settings, ok := instanceConfig(t, home)["redeem"].(map[string]any)
	require.True(t, ok)
	thresholdBaseUnits, ok := settings["threshold_base_units"].(int64)
	require.True(t, ok)
	require.Positive(t, thresholdBaseUnits)
	setInstanceConfig(t, home, thresholdBaseUnits, "prices", "channel_per_gb")

	return uint64(thresholdBaseUnits)
}

func setXrayRealityDest(t *testing.T, home string) {
	t.Helper()

	path := filepath.Join(home, xray.Name.String(), configFileName)

	raw, err := os.ReadFile(path) //nolint:gosec
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, toml.Unmarshal(raw, &doc))

	inbounds, ok := doc["inbounds"].([]any)
	require.True(t, ok)

	patched := false

	for _, entry := range inbounds {
		inbound, ok := entry.(map[string]any)
		require.True(t, ok)

		if inbound["security"] != "reality" {
			continue
		}

		reality, ok := inbound["reality"].(map[string]any)
		require.True(t, ok)

		reality["dest"] = realityDestAddr
		reality["server_name"] = realityServerName
		patched = true
	}

	require.True(t, patched, "node xray config has no reality inbound to patch")

	out, err := toml.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, out, 0o600))
}
