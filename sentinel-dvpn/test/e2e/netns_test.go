//go:build e2e && integration

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

const (
	bridgeCIDR         = "10.99.0.1/24"
	clientCIDR         = "10.99.0.4/24"
	internetBridgeCIDR = "198.51.100.1/24"
	internetCIDR       = "198.51.100.5/24"
	nodeCIDR           = "10.99.0.2/24"
	nsClient           = "e2e-client"
	nsInternet         = "e2e-internet"
	nsNode             = "e2e-node"
	nsProvider         = "e2e-provider"
	providerCIDR       = "10.99.0.3/24"
)

const (
	bridgeName         = "br-e2e0"
	internetBridgeName = "br-e2e1"
	maxLinkNameLen     = 15
)

const (
	dnsCounterName      = "dns_leak"
	forwardTable        = "e2e"
	internetCounterName = "internet_leak"
	natTable            = "e2enat"
	nftCleanupTimeout   = 5 * time.Second
	nftFamily           = "inet"
	nftTable            = "e2e"
)

type namespace struct {
	cidr     string
	handle   netns.NsHandle
	name     string
	vethName string
}

func newNamespace(t *testing.T, name, cidr, gateway string, bridge netlink.Link) *namespace {
	t.Helper()

	runtime.LockOSThread()

	defer runtime.UnlockOSThread()

	origin, err := netns.Get()
	require.NoError(t, err)

	closeOrigin := func() { _ = origin.Close() }
	defer closeOrigin()

	handle, err := netns.NewNamed(name)
	require.NoError(t, err)

	deleteNamespace := func() { deleteNamespaceIfExists(t, name) }
	t.Cleanup(deleteNamespace)

	require.NoError(t, netns.Set(origin))

	hostName, peerName := vethNames(name)
	require.LessOrEqual(t, len(hostName), maxLinkNameLen, hostName)
	peer := createVethPair(t, hostName, peerName, bridge)
	require.NoError(t, netlink.LinkSetNsFd(peer, int(handle)))

	nlHandle, err := netlink.NewHandleAt(handle)
	require.NoError(t, err)

	defer nlHandle.Close()

	nsPeer, err := nlHandle.LinkByName(peerName)
	require.NoError(t, err)

	parsedAddr, err := netlink.ParseAddr(cidr)
	require.NoError(t, err)
	require.NoError(t, nlHandle.AddrAdd(nsPeer, parsedAddr))
	require.NoError(t, nlHandle.LinkSetUp(nsPeer))
	require.NoError(t, nlHandle.RouteAdd(newDefaultRoute(nsPeer, gateway)))

	lo, err := nlHandle.LinkByName("lo")
	require.NoError(t, err)
	require.NoError(t, nlHandle.LinkSetUp(lo))

	ns := &namespace{
		cidr:     cidr,
		handle:   handle,
		name:     name,
		vethName: peerName,
	}

	return ns
}

func (ns *namespace) counter(t *testing.T, ctx context.Context, name string) uint64 {
	t.Helper()

	out := ns.run(t, ctx, "nft", "-j", "list", "counter", nftFamily, nftTable, name)

	return parseCounterJSON(t, out)
}

func (ns *namespace) installCounters(t *testing.T, ctx context.Context) {
	t.Helper()

	ns.run(t, ctx, "nft", "add", "table", nftFamily, nftTable)

	chainSpec := "{ type filter hook output priority 0 ; }"
	ns.run(t, ctx, "nft", "add", "chain", nftFamily, nftTable, "leak", chainSpec)
	ns.run(t, ctx, "nft", "add", "counter", nftFamily, nftTable, dnsCounterName)
	ns.run(t, ctx, "nft", "add", "counter", nftFamily, nftTable, internetCounterName)

	udpDNSRule := fmt.Sprintf("oifname %s udp dport 53 counter name %s", ns.vethName, dnsCounterName)
	ns.run(t, ctx, "nft", "add", "rule", nftFamily, nftTable, "leak", udpDNSRule)

	tcpDNSRule := fmt.Sprintf("oifname %s tcp dport 53 counter name %s", ns.vethName, dnsCounterName)
	ns.run(t, ctx, "nft", "add", "rule", nftFamily, nftTable, "leak", tcpDNSRule)

	internetRule := fmt.Sprintf(
		"oifname %s ip daddr %s counter name %s",
		ns.vethName,
		hostOf(internetCIDR),
		internetCounterName,
	)
	ns.run(t, ctx, "nft", "add", "rule", nftFamily, nftTable, "leak", internetRule)
}

func (ns *namespace) run(t *testing.T, ctx context.Context, name string, args ...string) []byte {
	t.Helper()

	return runInNamespace(t, ctx, ns.handle, name, args...)
}

func (ns *namespace) start(t *testing.T, ctx context.Context, logPath, name string, args ...string) *exec.Cmd {
	t.Helper()

	return startInNamespace(t, ctx, ns.handle, logPath, name, args...)
}

func (ns *namespace) tables(t *testing.T, ctx context.Context) []string {
	t.Helper()

	out := ns.run(t, ctx, "nft", "-j", "list", "tables")

	return parseTablesJSON(t, out)
}

type topology struct {
	bridge   netlink.Link
	client   *namespace
	internet *namespace
	node     *namespace
	provider *namespace
}

func newTopology(t *testing.T, ctx context.Context) *topology {
	t.Helper()

	bridge := createBridge(t, bridgeName, bridgeCIDR)
	internetBridge := createBridge(t, internetBridgeName, internetBridgeCIDR)

	installForwardPolicy(t, ctx)
	setSysctl(t, "/proc/sys/net/ipv4/ip_forward", "1")

	node := newNamespace(t, nsNode, nodeCIDR, bridgeCIDR, bridge)
	provider := newNamespace(t, nsProvider, providerCIDR, bridgeCIDR, bridge)
	client := newNamespace(t, nsClient, clientCIDR, bridgeCIDR, bridge)
	internet := newNamespace(t, nsInternet, internetCIDR, internetBridgeCIDR, internetBridge)

	client.installCounters(t, ctx)

	tp := &topology{
		bridge:   bridge,
		client:   client,
		internet: internet,
		node:     node,
		provider: provider,
	}

	return tp
}

func TestDeleteHostNFTTable_CanceledContext(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "nft.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$SENTINEL_DVPN_E2E_NFT_LOG\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nft"), []byte(script), 0o700)) //nolint:gosec
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SENTINEL_DVPN_E2E_NFT_LOG", logPath)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	deleteHostNFTTable(t, ctx, "test-cleanup")

	args, err := os.ReadFile(logPath) //nolint:gosec // log belongs to this test's temporary directory
	require.NoError(t, err)
	require.Equal(t, "delete table inet test-cleanup\n", string(args))
}

func configureNodeNAT(t *testing.T, ctx context.Context, node *namespace) {
	t.Helper()

	node.run(t, ctx, "sh", "-c", "echo 1 > /proc/sys/net/ipv4/ip_forward")
	node.run(t, ctx, "nft", "add", "table", nftFamily, natTable)

	chainSpec := "{ type nat hook postrouting priority 100 ; }"
	node.run(t, ctx, "nft", "add", "chain", nftFamily, natTable, "postrouting", chainSpec)

	rule := fmt.Sprintf("oifname %s masquerade", node.vethName)
	node.run(t, ctx, "nft", "add", "rule", nftFamily, natTable, "postrouting", rule)
}

func createBridge(t *testing.T, name, cidr string) netlink.Link {
	t.Helper()

	attrs := netlink.NewLinkAttrs()
	attrs.Name = name

	br := &netlink.Bridge{LinkAttrs: attrs}
	require.NoError(t, netlink.LinkAdd(br))

	deleteBridge := func() { deleteLinkIfExists(t, name) }
	t.Cleanup(deleteBridge)

	addr, err := netlink.ParseAddr(cidr)
	require.NoError(t, err)
	require.NoError(t, netlink.AddrAdd(br, addr))
	require.NoError(t, netlink.LinkSetUp(br))

	return br
}

func createVethPair(t *testing.T, hostName, peerName string, bridge netlink.Link) netlink.Link {
	t.Helper()

	attrs := netlink.NewLinkAttrs()
	attrs.Name = hostName

	veth := &netlink.Veth{
		LinkAttrs: attrs,
		PeerName:  peerName,
	}
	require.NoError(t, netlink.LinkAdd(veth))

	deleteHostSide := func() { deleteLinkIfExists(t, hostName) }
	t.Cleanup(deleteHostSide)

	host, err := netlink.LinkByName(hostName)
	require.NoError(t, err)
	require.NoError(t, netlink.LinkSetMaster(host, bridge))
	require.NoError(t, netlink.LinkSetUp(host))

	peer, err := netlink.LinkByName(peerName)
	require.NoError(t, err)

	return peer
}

func deleteHostNFTTable(t *testing.T, ctx context.Context, table string) {
	t.Helper()

	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), nftCleanupTimeout)
	defer cancel()

	cmd := exec.CommandContext(cleanupCtx, "nft", "delete", "table", nftFamily, table)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("deleting host nft table %s: %v: %s", table, err, out)
	}
}

func deleteLinkIfExists(t *testing.T, name string) {
	t.Helper()

	link, err := netlink.LinkByName(name)
	if err != nil {
		return
	}

	if err := netlink.LinkDel(link); err != nil {
		t.Logf("deleting link %s: %v", name, err)
	}
}

func deleteNamespaceIfExists(t *testing.T, name string) {
	t.Helper()

	if err := netns.DeleteNamed(name); err != nil {
		t.Logf("deleting namespace %s: %v", name, err)
	}
}

func forwardRules() []string {
	segments := fmt.Sprintf(`{ "%s", "%s" }`, bridgeName, internetBridgeName)

	return []string{
		fmt.Sprintf("iifname %s oifname %s accept", segments, segments),
		fmt.Sprintf("iifname %s drop", segments),
	}
}

func hostOf(cidr string) string {
	host, _, _ := strings.Cut(cidr, "/")

	return host
}

func installForwardPolicy(t *testing.T, ctx context.Context) {
	t.Helper()

	runHostNFT(t, ctx, "add", "table", nftFamily, forwardTable)

	deleteHostTable := func() { deleteHostNFTTable(t, ctx, forwardTable) }
	t.Cleanup(deleteHostTable)

	chainSpec := "{ type filter hook forward priority 0 ; policy accept ; }"
	runHostNFT(t, ctx, "add", "chain", nftFamily, forwardTable, "forward", chainSpec)

	for _, rule := range forwardRules() {
		runHostNFT(t, ctx, "add", "rule", nftFamily, forwardTable, "forward", rule)
	}
}

func installRPCNAT(t *testing.T, ctx context.Context, rpcPort, wsPort int) {
	t.Helper()

	setSysctl(t, "/proc/sys/net/ipv4/conf/all/route_localnet", "1")

	runHostNFT(t, ctx, "add", "table", nftFamily, natTable)

	deleteHostTable := func() { deleteHostNFTTable(t, ctx, natTable) }
	t.Cleanup(deleteHostTable)

	chainSpec := "{ type nat hook prerouting priority -100 ; }"
	runHostNFT(t, ctx, "add", "chain", nftFamily, natTable, "prerouting", chainSpec)

	rpcRule := fmt.Sprintf("ip daddr %s tcp dport %d dnat to 127.0.0.1:%d", hostOf(bridgeCIDR), rpcPort, rpcPort)
	runHostNFT(t, ctx, "add", "rule", nftFamily, natTable, "prerouting", rpcRule)

	wsRule := fmt.Sprintf("ip daddr %s tcp dport %d dnat to 127.0.0.1:%d", hostOf(bridgeCIDR), wsPort, wsPort)
	runHostNFT(t, ctx, "add", "rule", nftFamily, natTable, "prerouting", wsRule)
}

func newDefaultRoute(link netlink.Link, gateway string) *netlink.Route {
	route := &netlink.Route{
		Gw:        net.ParseIP(hostOf(gateway)),
		LinkIndex: link.Attrs().Index,
	}

	return route
}

func parseCounterJSON(t *testing.T, raw []byte) uint64 {
	t.Helper()

	type counterDoc struct {
		Nftables []struct {
			Counter struct {
				Packets uint64 `json:"packets"`
			} `json:"counter"`
		} `json:"nftables"`
	}

	var doc counterDoc

	require.NoError(t, json.Unmarshal(raw, &doc))
	require.NotEmpty(t, doc.Nftables)

	return doc.Nftables[0].Counter.Packets
}

func parseTablesJSON(t *testing.T, raw []byte) []string {
	t.Helper()

	type tablesDoc struct {
		Nftables []struct {
			Table *struct {
				Family string `json:"family"`
				Name   string `json:"name"`
			} `json:"table"`
		} `json:"nftables"`
	}

	var doc tablesDoc

	require.NoError(t, json.Unmarshal(raw, &doc))

	var tables []string

	for _, item := range doc.Nftables {
		if item.Table != nil {
			tables = append(tables, item.Table.Family+" "+item.Table.Name)
		}
	}

	return tables
}

func runHostNFT(t *testing.T, ctx context.Context, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(ctx, "nft", args...)

	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "nft %s: %s", strings.Join(args, " "), out)
}

func runInNamespace(t *testing.T, ctx context.Context, handle netns.NsHandle, name string, args ...string) []byte {
	t.Helper()

	runtime.LockOSThread()

	defer runtime.UnlockOSThread()

	origin, err := netns.Get()
	require.NoError(t, err)

	closeOrigin := func() { _ = origin.Close() }
	defer closeOrigin()

	restoreOrigin := func() { _ = netns.Set(origin) }
	defer restoreOrigin()

	require.NoError(t, netns.Set(handle))

	cmd := exec.CommandContext(ctx, name, args...)

	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "%s %s: %s", name, strings.Join(args, " "), out)

	return out
}

func setSysctl(t *testing.T, path, value string) {
	t.Helper()

	previous, err := os.ReadFile(path) //nolint:gosec
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(path, []byte(value), 0o600))

	restore := func() {
		if err := os.WriteFile(path, previous, 0o600); err != nil { //nolint:gosec
			t.Errorf("restoring %s: %v", path, err)
		}
	}
	t.Cleanup(restore)
}

func startInNamespace(
	t *testing.T,
	ctx context.Context,
	handle netns.NsHandle,
	logPath,
	name string,
	args ...string,
) *exec.Cmd {
	t.Helper()

	runtime.LockOSThread()

	defer runtime.UnlockOSThread()

	origin, err := netns.Get()
	require.NoError(t, err)

	closeOrigin := func() { _ = origin.Close() }
	defer closeOrigin()

	restoreOrigin := func() { _ = netns.Set(origin) }
	defer restoreOrigin()

	require.NoError(t, netns.Set(handle))

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Cancel = func() error {
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("stopping test process: %w", err)
		}

		return nil
	}
	cmd.WaitDelay = stopTimeout

	logFile, err := os.Create(logPath) //nolint:gosec
	require.NoError(t, err)
	t.Cleanup(func() { _ = logFile.Close() })

	cmd.Stdout = logFile
	cmd.Stderr = logFile

	require.NoError(t, cmd.Start())

	return cmd
}

func vethNames(name string) (string, string) {
	peer := "v-" + strings.TrimPrefix(name, "e2e-")

	return peer + "h", peer
}
