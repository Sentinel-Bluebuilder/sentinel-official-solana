//go:build e2e && integration

package e2e_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netns"

	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
	daemonv1 "github.com/sentinel-official/sentinel-go-sdk/v3/daemon/v1"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/version"
)

const (
	buildTimeout = 5 * time.Minute
	stopTimeout  = 10 * time.Second
)

type cliRunner struct {
	bin      string
	endpoint string
	home     string
	keyName  string
	rpcURL   string
	tag      instance.Tag
	wsURL    string
}

func (c cliRunner) run(t *testing.T, ctx context.Context, args ...string) []byte {
	t.Helper()

	full := c.withGlobals(args)

	return runCLI(t, ctx, c.bin, full...)
}

func (c cliRunner) try(ctx context.Context, args ...string) ([]byte, error) {
	full := c.withGlobals(args)

	return tryCLI(ctx, c.bin, full...)
}

func (c cliRunner) withDaemon(tag instance.Tag, dir string) cliRunner {
	c.endpoint = filepath.Join(dir, daemonv1.SocketFileName)
	c.tag = tag

	return c
}

func (c cliRunner) withGlobals(args []string) []string {
	globals := []string{"--home", c.home, "--rpc.url", c.rpcURL, "--rpc.ws-url", c.wsURL}

	if c.keyName != "" {
		globals = append(globals, "--key.name", c.keyName)
	}

	if c.endpoint != "" {
		globals = append(globals, "--daemon.endpoint", c.endpoint)
	}

	if c.tag != "" {
		globals = append(globals, "--instance", string(c.tag))
	}

	return append(globals, args...)
}

func (c cliRunner) withKey(name string) cliRunner {
	c.keyName = name

	return c
}

func TestStartInNamespace_CanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	marker := filepath.Join(t.TempDir(), "stopped")
	ready := filepath.Join(t.TempDir(), "ready")
	script := "trap 'printf stopped > \"$1\"; exit 0' TERM; printf ready > \"$2\"; while :; do sleep 1; done"
	handle, err := netns.Get()
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Close() })

	cmd := startInNamespace(t, ctx, handle, filepath.Join(t.TempDir(), "process.log"),
		"sh", "-c", script, "test-process", marker, ready)
	t.Cleanup(func() { stopProcess(cmd, stopTimeout) })

	waitForCondition(t, func() (bool, error) {
		_, err := os.Stat(ready)
		if err != nil {
			return false, fmt.Errorf("checking process ready marker: %w", err)
		}

		return true, nil
	})
	cancel()
	require.ErrorIs(t, cmd.Wait(), context.Canceled)

	content, err := os.ReadFile(marker) //nolint:gosec
	require.NoError(t, err)
	require.Equal(t, "stopped", string(content))
}

func buildBinaries(t *testing.T, ctx context.Context, cliDir string, localnet *sdktest.Localnet) (string, string) {
	t.Helper()

	requireBinary(t, "make")
	daemonDir := daemonSourceDir(t)
	requireDir(t, daemonDir)

	runMake(t, ctx, daemonDir, localnet)
	runMake(t, ctx, cliDir, localnet)

	daemonBin := filepath.Join(daemonDir, "bin", "sentinel-dvpnd")
	cliBin := filepath.Join(cliDir, "bin", "sentinel-dvpn")

	return daemonBin, cliBin
}

func cliSourceDir(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	require.NoError(t, err)

	return filepath.Dir(filepath.Dir(wd))
}

func daemonSourceDir(t *testing.T) string {
	t.Helper()

	if dir := os.Getenv("SENTINEL_DVPN_E2E_DAEMON_SOURCE_DIR"); dir != "" {
		return dir
	}

	wd, err := os.Getwd()
	require.NoError(t, err)

	repoRoot := filepath.Dir(filepath.Dir(wd))

	return filepath.Join(repoRoot, "..", "sentinel-dvpnd")
}

func runMake(t *testing.T, ctx context.Context, dir string, localnet *sdktest.Localnet) {
	t.Helper()

	mctx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()

	args := []string{
		"build",
		"SENTINEL_NETWORK=localnet",
		"SENTINEL_PROGRAM_ID=" + localnet.ProgramID.String(),
		"SENTINEL_MINT=" + localnet.Mint.String(),
	}
	cmd := exec.CommandContext(mctx, "make", args...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make build (dir=%s): %v\n%s", dir, err, out)
	}
}

func startDaemon(t *testing.T, ctx context.Context, ns *namespace, home, daemonBin string, args ...string) *exec.Cmd {
	t.Helper()

	logPath := filepath.Join(home, "daemon.log")

	return startDaemonWithLog(t, ctx, ns, logPath, daemonBin, args...)
}

func startDaemonWithLog(t *testing.T, ctx context.Context, ns *namespace, logPath, daemonBin string,
	args ...string,
) *exec.Cmd {
	t.Helper()

	proc := ns.start(t, ctx, logPath, daemonBin, args...)

	dumpOnFailure := func() {
		if !t.Failed() {
			return
		}

		out, err := os.ReadFile(logPath) //nolint:gosec
		if err != nil {
			return
		}

		t.Logf("%s %s output:\n%s", filepath.Base(filepath.Dir(logPath)), filepath.Base(logPath), out)
	}
	t.Cleanup(dumpOnFailure)

	return proc
}

func stopDaemon(t *testing.T, proc *exec.Cmd, role string) {
	t.Helper()

	if timedOut := stopProcess(proc, stopTimeout); timedOut {
		t.Errorf("%s daemon did not exit within %s of SIGTERM", role, stopTimeout)
	}
}

func stopProcess(proc *exec.Cmd, timeout time.Duration) bool {
	if proc.Process == nil || proc.ProcessState != nil {
		return false
	}

	_ = proc.Process.Signal(syscall.SIGTERM)

	done := make(chan struct{}, 1)

	go func() { _ = proc.Wait(); done <- struct{}{} }()

	select {
	case <-done:
		return false
	case <-time.After(timeout):
		_ = proc.Process.Kill()

		<-done

		return true
	}
}

func verifyVersions(t *testing.T, ctx context.Context, localnet *sdktest.Localnet, daemonBin, cliBin string) {
	t.Helper()

	daemonOut := runCLI(t, ctx, daemonBin, "version")
	daemonInfo := decodeJSON[version.Info](t, daemonOut)
	require.Equal(t, localnet.ProgramID.String(), daemonInfo.ProgramID)
	require.Equal(t, localnet.Mint.String(), daemonInfo.Mint)

	cliOut := runCLI(t, ctx, cliBin, "version")
	cliInfo := decodeJSON[version.Info](t, cliOut)
	require.Equal(t, localnet.ProgramID.String(), cliInfo.ProgramID)
	require.Equal(t, localnet.Mint.String(), cliInfo.Mint)
}
