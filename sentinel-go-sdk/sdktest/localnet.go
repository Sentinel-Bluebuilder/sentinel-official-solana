//go:build integration

package sdktest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	associatedtokenaccount "github.com/solana-foundation/solana-go/v2/programs/associated-token-account"
	"github.com/solana-foundation/solana-go/v2/programs/system"
	"github.com/solana-foundation/solana-go/v2/programs/token-2022"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/solana-foundation/solana-go/v2/rpc/ws"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const (
	MinNodeBond     uint64 = 1_000_000_000
	MinProviderBond uint64 = 10_000_000_000
)

const (
	airdropSOL           = 500
	decimals             = 6
	dynamicPortRangeSize = 32
	lamportsPerSOL       = 1_000_000_000
	maxStartAttempts     = 3
	startupTimeout       = 90 * time.Second
	stderrTailBytes      = 4096
	supply               = 1_000_000_000 * 1_000_000
)

var (
	errValidatorExited        = errors.New("solana-test-validator exited before becoming healthy")
	errValidatorExitedCleanly = errors.New("exited with status 0")
)

//nolint:gochecknoglobals
var reservedPorts = portSet{taken: map[int]bool{}}

type Localnet struct {
	Authority solana.PrivateKey
	Faucet    solana.PrivateKey
	Mint      solana.PublicKey
	ProgramID solana.PublicKey
	RPC       *rpc.Client
	RPCURL    string
	Treasury  solana.PublicKey
	WS        *ws.Client
	WSURL     string

	txConfig tx.Config
}

func (l *Localnet) FundSOL(ctx context.Context, recipient solana.PublicKey, sol uint64) error {
	sig, err := l.RPC.RequestAirdrop(ctx, recipient, sol*lamportsPerSOL, rpc.CommitmentConfirmed)
	if err != nil {
		return fmt.Errorf("requesting airdrop: %w", err)
	}

	return waitSignature(ctx, l.RPC, sig)
}

func (l *Localnet) FundTokens(ctx context.Context, owner solana.PublicKey, amount uint64) error {
	ata, _, err := solana.FindAssociatedTokenAddressWithProgram(owner, l.Mint, solana.Token2022ProgramID)
	if err != nil {
		return fmt.Errorf("deriving owner ata: %w", err)
	}

	faucetATA, _, err := solana.FindAssociatedTokenAddressWithProgram(
		l.Faucet.PublicKey(),
		l.Mint,
		solana.Token2022ProgramID,
	)
	if err != nil {
		return fmt.Errorf("deriving faucet ata: %w", err)
	}

	createATAIx := associatedtokenaccount.NewCreateIdempotentInstructionWithTokenProgram(
		l.Faucet.PublicKey(),
		owner,
		l.Mint,
		solana.Token2022ProgramID,
	).Build()
	transferIx := token2022.NewTransferCheckedInstruction(
		amount,
		decimals,
		faucetATA,
		l.Mint,
		ata,
		l.Faucet.PublicKey(),
		nil,
	).Build()
	ixs := []solana.Instruction{createATAIx, transferIx}

	if _, err := tx.Send(ctx, l.RPC, l.WS, l.Faucet, l.txConfig, ixs); err != nil {
		return fmt.Errorf("funding tokens: %w", err)
	}

	return nil
}

func (l *Localnet) createMint(ctx context.Context, mint solana.PrivateKey) error {
	rent, err := l.RPC.GetMinimumBalanceForRentExemption(ctx, token2022.MINT_SIZE, rpc.CommitmentConfirmed)
	if err != nil {
		return fmt.Errorf("fetching rent exemption: %w", err)
	}

	faucetATA, _, err := solana.FindAssociatedTokenAddressWithProgram(
		l.Faucet.PublicKey(),
		l.Mint,
		solana.Token2022ProgramID,
	)
	if err != nil {
		return fmt.Errorf("deriving faucet ata: %w", err)
	}

	createAccountIx := system.NewCreateAccountInstruction(
		rent,
		token2022.MINT_SIZE,
		solana.Token2022ProgramID,
		l.Authority.PublicKey(),
		mint.PublicKey(),
	).Build()
	initMintIx := token2022.NewInitializeMint2InstructionBuilder().
		SetDecimals(decimals).
		SetMintAuthority(l.Authority.PublicKey()).
		SetMintAccount(mint.PublicKey()).
		Build()
	createFaucetATAIx := associatedtokenaccount.NewCreateIdempotentInstructionWithTokenProgram(
		l.Authority.PublicKey(),
		l.Faucet.PublicKey(),
		l.Mint,
		solana.Token2022ProgramID,
	).Build()
	mintToIx := token2022.NewMintToInstruction(supply, mint.PublicKey(), faucetATA, l.Authority.PublicKey(), nil).Build()
	revokeMintAuthorityIx := token2022.NewSetAuthorityInstructionBuilder().
		SetAuthorityType(token2022.AuthorityMintTokens).
		SetSubjectAccount(mint.PublicKey()).
		SetAuthorityAccount(l.Authority.PublicKey()).
		Build()
	ixs := []solana.Instruction{createAccountIx, initMintIx, createFaucetATAIx, mintToIx, revokeMintAuthorityIx}

	if _, err := tx.Send(ctx, l.RPC, l.WS, l.Authority, l.txConfig, ixs, tx.WithSigners(mint)); err != nil {
		return fmt.Errorf("creating mint: %w", err)
	}

	return nil
}

func (l *Localnet) initializeConfig(ctx context.Context) error {
	programClient := program.NewClient(l.RPC, l.ProgramID, l.Mint, rpc.CommitmentConfirmed)
	if _, err := programClient.Config(ctx); err == nil {
		return nil
	}

	treasuryATA, _, err := solana.FindAssociatedTokenAddressWithProgram(l.Treasury, l.Mint, solana.Token2022ProgramID)
	if err != nil {
		return fmt.Errorf("deriving treasury ata: %w", err)
	}

	config, _, err := dvpn.FindConfigAddress(l.ProgramID)
	if err != nil {
		return fmt.Errorf("deriving config address: %w", err)
	}

	programData, err := core.FindProgramDataAddress(l.ProgramID)
	if err != nil {
		return err
	}

	initParams := dvpn.InitializeConfigParams{
		FeeBps:                   200,
		MinNodeBond:              MinNodeBond,
		MinProviderBond:          MinProviderBond,
		UnbondingSeconds:         604_800,
		ChannelCloseGraceSeconds: 86_400,
		LeaseCloseGraceSeconds:   86_400,
		NodeListingTTLSeconds:    86_400,
		PausedMask:               dvpn.PauseMask,
		Authority:                l.Authority.PublicKey(),
		ProgramData:              programData,
		Mint:                     l.Mint,
		Treasury:                 treasuryATA,
		RentPayer:                l.Authority.PublicKey(),
	}

	initIx, err := dvpn.BuildInitializeConfigInstruction(l.ProgramID, initParams)
	if err != nil {
		return fmt.Errorf("building initialize config instruction: %w", err)
	}

	unpauseParams := dvpn.SetPausedMaskParams{
		Authority:  l.Authority.PublicKey(),
		PausedMask: 0,
	}

	unpauseIx, err := dvpn.BuildSetPausedMaskInstruction(l.ProgramID, unpauseParams)
	if err != nil {
		return fmt.Errorf("building set paused mask instruction: %w", err)
	}

	createTreasuryATAIx := associatedtokenaccount.NewCreateIdempotentInstructionWithTokenProgram(
		l.Authority.PublicKey(),
		l.Treasury,
		l.Mint,
		solana.Token2022ProgramID,
	).Build()
	createConfigATAIx := associatedtokenaccount.NewCreateIdempotentInstructionWithTokenProgram(
		l.Authority.PublicKey(),
		config,
		l.Mint,
		solana.Token2022ProgramID,
	).Build()
	ixs := []solana.Instruction{createTreasuryATAIx, createConfigATAIx, initIx, unpauseIx}

	if _, err := tx.Send(ctx, l.RPC, l.WS, l.Authority, l.txConfig, ixs); err != nil {
		return fmt.Errorf("initializing config: %w", err)
	}

	return nil
}

type portSet struct {
	mu    sync.Mutex
	taken map[int]bool
}

func (s *portSet) reserve(ports ...int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, port := range ports {
		if s.taken[port] {
			return false
		}
	}

	for _, port := range ports {
		s.taken[port] = true
	}

	return true
}

type procExit struct {
	done chan struct{}
	err  error
}

func newProcExit(cmd *exec.Cmd) *procExit {
	exit := &procExit{done: make(chan struct{})}

	wait := func() {
		exit.err = cmd.Wait()
		close(exit.done)
	}
	go wait()

	return exit
}

type tailWriter struct {
	buf []byte
	mu  sync.Mutex
}

func (w *tailWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()

	return string(w.buf)
}

func (w *tailWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buf = append(w.buf, data...)
	if len(w.buf) > stderrTailBytes {
		w.buf = w.buf[len(w.buf)-stderrTailBytes:]
	}

	return len(data), nil
}

func StartLocalnet(t *testing.T) *Localnet {
	t.Helper()

	programPath := os.Getenv("SENTINEL_DVPN_SO")
	if programPath == "" {
		programPath = filepath.Join(os.Getenv("SENTINEL_PROGRAMS_DIR"), "target", "deploy", "dvpn.so")
	}

	if _, err := os.Stat(programPath); err != nil { //nolint:gosec
		skipOrFail(t, "dvpn.so not found at %s", programPath)
	}

	if _, err := exec.LookPath("solana-test-validator"); err != nil {
		skipOrFail(t, "solana-test-validator not on PATH")
	}

	authority := newPrivateKey(t)
	faucet := newPrivateKey(t)
	mint := newPrivateKey(t)
	treasury := newPrivateKey(t)
	programID := dvpn.ProgramID

	var (
		localnet *Localnet
		err      error
	)

	for range maxStartAttempts {
		localnet, err = startValidator(t, programPath, programID, authority, faucet, treasury, mint)
		if !errors.Is(err, errValidatorExited) {
			break
		}
	}

	require.NoError(t, err)

	ctx := t.Context()

	require.NoError(t, localnet.FundSOL(ctx, authority.PublicKey(), airdropSOL))
	require.NoError(t, localnet.FundSOL(ctx, faucet.PublicKey(), airdropSOL))
	require.NoError(t, localnet.createMint(ctx, mint))
	require.NoError(t, localnet.initializeConfig(ctx))

	return localnet
}

func newPrivateKey(t *testing.T) solana.PrivateKey {
	t.Helper()

	key, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	return key
}

func rangeFree(start, count int) bool {
	var listenConfig net.ListenConfig

	lns := make([]io.Closer, 0, count*2)
	defer func() {
		for _, ln := range lns {
			_ = ln.Close()
		}
	}()

	for port := start; port < start+count; port++ {
		addr := fmt.Sprintf("127.0.0.1:%d", port)

		tcpLn, err := listenConfig.Listen(context.Background(), "tcp", addr)
		if err != nil {
			return false
		}

		lns = append(lns, tcpLn)

		udpLn, err := listenConfig.ListenPacket(context.Background(), "udp", addr)
		if err != nil {
			return false
		}

		lns = append(lns, udpLn)
	}

	return true
}

func reserveGossipPort(t *testing.T) int {
	t.Helper()

	var listenConfig net.ListenConfig

	for {
		listener, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)

		port := listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()

		if !udpPortFree(port) {
			continue
		}

		if reservedPorts.reserve(port) {
			return port
		}
	}
}

func reservePort(t *testing.T) int {
	t.Helper()

	var listenConfig net.ListenConfig

	for {
		listener, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)

		port := listener.Addr().(*net.TCPAddr).Port
		_ = listener.Close()

		if reservedPorts.reserve(port) {
			return port
		}
	}
}

func reservePortPair(t *testing.T) int {
	t.Helper()

	var listenConfig net.ListenConfig

	for {
		rpcLn, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)

		rpcPort := rpcLn.Addr().(*net.TCPAddr).Port
		wsLn, wsErr := listenConfig.Listen(context.Background(), "tcp", fmt.Sprintf("127.0.0.1:%d", rpcPort+1))
		_ = rpcLn.Close()

		if wsErr != nil {
			continue
		}

		_ = wsLn.Close()

		if !reservedPorts.reserve(rpcPort, rpcPort+1) {
			continue
		}

		return rpcPort
	}
}

func reservePortRange(t *testing.T, count int) (int, int) {
	t.Helper()

	var listenConfig net.ListenConfig

	for {
		first, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)

		start := first.Addr().(*net.TCPAddr).Port
		_ = first.Close()

		if !rangeFree(start, count) {
			continue
		}

		ports := make([]int, count)
		for i := range ports {
			ports[i] = start + i
		}

		if !reservedPorts.reserve(ports...) {
			continue
		}

		return start, start + count - 1
	}
}

func skipOrFail(t *testing.T, format string, args ...any) { //nolint:goprintffuncname
	t.Helper()

	msg := fmt.Sprintf(format, args...)
	if os.Getenv("SENTINEL_REQUIRE_LOCALNET") == "1" {
		t.Fatal(msg)
	}

	t.Skip(msg)
}

func startValidator(
	t *testing.T,
	programPath string,
	programID solana.PublicKey,
	authority,
	faucet,
	treasury,
	mint solana.PrivateKey,
) (*Localnet, error) {
	t.Helper()

	rpcPort := reservePortPair(t)
	faucetPort := reservePort(t)
	gossipPort := reserveGossipPort(t)
	dynamicPortStart, dynamicPortEnd := reservePortRange(t, dynamicPortRangeSize)
	ledger := t.TempDir()

	args := []string{
		"--reset",
		"--quiet",
		"--bind-address", "127.0.0.1",
		"--ledger", ledger,
		"--rpc-port", strconv.Itoa(rpcPort),
		"--faucet-port", strconv.Itoa(faucetPort),
		"--gossip-port", strconv.Itoa(gossipPort),
		"--dynamic-port-range", fmt.Sprintf("%d-%d", dynamicPortStart, dynamicPortEnd),
		"--upgradeable-program", programID.String(),
		programPath,
		authority.PublicKey().String(),
	}

	procCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(procCtx, "solana-test-validator", args...) //nolint:gosec

	tail := &tailWriter{}
	cmd.Stdout = io.MultiWriter(os.Stderr, tail)
	cmd.Stderr = cmd.Stdout
	require.NoError(t, cmd.Start())

	proc := newProcExit(cmd)

	stop := func() {
		cancel()

		<-proc.done
	}
	t.Cleanup(stop)

	localnet := &Localnet{
		Authority: authority,
		Faucet:    faucet,
		Mint:      mint.PublicKey(),
		ProgramID: programID,
		RPCURL:    fmt.Sprintf("http://127.0.0.1:%d", rpcPort),
		Treasury:  treasury.PublicKey(),
		WSURL:     fmt.Sprintf("ws://127.0.0.1:%d", rpcPort+1),
		txConfig:  tx.DefaultConfig(),
	}
	localnet.RPC = rpc.NewWithTimeoutAndCommitment(localnet.RPCURL, 20*time.Second, rpc.CommitmentConfirmed)

	ctx := t.Context()

	if err := waitHealthy(ctx, localnet.RPC, proc, tail); err != nil {
		return nil, err
	}

	wsClient, err := ws.Connect(ctx, localnet.WSURL)
	if err != nil {
		return nil, fmt.Errorf("connecting websocket: %w", err)
	}

	localnet.WS = wsClient

	return localnet, nil
}

func udpPortFree(port int) bool {
	var listenConfig net.ListenConfig

	listener, err := listenConfig.ListenPacket(context.Background(), "udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}

	_ = listener.Close()

	return true
}

func waitHealthy(ctx context.Context, rpcClient *rpc.Client, proc *procExit, tail *tailWriter) error {
	ctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()

	probeCtx, probeCancel := context.WithCancel(ctx)
	defer probeCancel()

	probe := func(ctx context.Context) error {
		if _, err := rpcClient.GetHealth(ctx); err != nil {
			return fmt.Errorf("checking health: %w", err)
		}

		return nil
	}

	done := make(chan error, 1)

	poll := func() { done <- service.PollReady(probeCtx, probe) }
	go poll()

	select {
	case err := <-done:
		return err
	case <-proc.done:
		probeCancel()
		<-done //nolint:errcheck // waits out the canceled probe; the process exit error is reported instead

		exitErr := proc.err
		if exitErr == nil {
			exitErr = errValidatorExitedCleanly
		}

		return fmt.Errorf("%w: %w\n%s", errValidatorExited, exitErr, tail.String())
	}
}

func waitSignature(ctx context.Context, rpcClient *rpc.Client, sig solana.Signature) error {
	ctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()

	probe := func(ctx context.Context) error {
		resp, err := rpcClient.GetSignatureStatuses(ctx, true, sig)
		if err != nil {
			return fmt.Errorf("fetching signature status: %w", err)
		}

		if len(resp.Value) != 1 || resp.Value[0] == nil || resp.Value[0].ConfirmationStatus == "" {
			return fmt.Errorf("signature %s not confirmed", sig)
		}

		return nil
	}

	if err := service.PollReady(ctx, probe); err != nil {
		return fmt.Errorf("confirming %s: %w", sig, err)
	}

	return nil
}
