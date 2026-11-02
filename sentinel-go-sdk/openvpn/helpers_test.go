package openvpn_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/openvpn"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

const (
	adapterName     = "dvpn-c0"
	binDir          = "bin"
	statusV3Fixture = "TITLE" +
		"\tOpenVPN 1.2.3 test-platform [SSL (OpenSSL)] [LZO] [LZ4] [EPOLL] [PKCS11] [MH/P" +
		"KTINFO] [AEAD] [DCO]\n" +
		"TIME\tThu Jan 1 00:00:00 1970\t0\n" +
		"HEADER\tCLIENT_LIST\tCommon Name\tReal Address\tVirtual Address" +
		"\tVirtual IPv6 Address\tBytes Received\tBytes Sent\tConnected Since" +
		"\tConnected Since (time_t)\tUsername\tClient ID\tPeer ID\tData Channel Cipher\n" +
		"CLIENT_LIST\tuser-1\t203.0.113.5:12345\t10.0.0.2\t\t1500\t2500" +
		"\tThu Jan 1 00:00:00 1970\t0\tuser-1\t7\t0\tAES-256-GCM\n" +
		"HEADER\tROUTING_TABLE\tVirtual Address\tCommon Name\tReal Address\tLast Ref\tLast Ref (time_t)\n" +
		"ROUTING_TABLE\t10.0.0.2\tuser-1\t203.0.113.5:12345\tThu Jan 1 00:00:00 1970\t0\n" +
		"GLOBAL_STATS\tMax bcast/mcast queue length\t0\n" +
		"END\n"
)

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func fakeLookPath(string) (string, error) {
	return filepath.Join(binDir, "openvpn.exe"), nil
}

func metadataFixture(t *testing.T) openvpn.Metadata {
	t.Helper()

	cert, _, err := api.LoadOrCreateServerCert(t.TempDir())
	require.NoError(t, err)

	tlsCrypt, err := os.ReadFile("testdata/tls-crypt.key")
	require.NoError(t, err)

	return openvpn.Metadata{
		CA:       string(api.EncodeCertificatePEM(cert)),
		Port:     1194,
		Protocol: "udp",
		TLSCrypt: string(tlsCrypt),
	}
}

func newFakeStarter(
	t *testing.T, process *service.Process, input *os.File,
) func(ctx context.Context, cmd service.Command, log zerolog.Logger) (service.Process, error) {
	t.Helper()

	return func(ctx context.Context, cmd service.Command, log zerolog.Logger) (service.Process, error) {
		cmd.Path = os.Args[0]
		cmd.Args = []string{"-test.run=^TestOpenVPNChildProcess$"}
		cmd.Env = []string{"OPENVPN_TEST_CHILD=1"}
		cmd.Stdin = input
		p, err := service.StartProcess(ctx, cmd, log)
		*process = p

		return p, err
	}
}

func newProcessClient(
	t *testing.T, listener net.Listener, unnamed bool,
) (*openvpn.Client, func() service.Process, *os.File, service.Placement) {
	t.Helper()

	input, output, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = input.Close(); _ = output.Close() })

	cfg := openvpn.DefaultClientConfig()
	p := service.Placement{Dir: t.TempDir(), Interface: "dvpn-c0", ToolDir: t.TempDir()}

	if unnamed {
		p.Interface = ""
	}

	var process service.Process

	cfg.ManagementPort = uint16(listener.Addr().(*net.TCPAddr).Port)

	client := openvpn.NewClientForTest(
		cfg, p, zerolog.Nop(),
		service.WithProcessStarter(newFakeStarter(t, &process, input)),
	)

	return client, func() service.Process { return process }, output, p
}

func payloadFixture(t *testing.T) openvpn.PeerResponse {
	t.Helper()

	m := metadataFixture(t)

	return openvpn.PeerResponse{
		CA:       m.CA,
		Password: strings.Repeat("ab", 32),
		Port:     m.Port,
		Protocol: m.Protocol,
		TLSCrypt: m.TLSCrypt,
		Username: "00000000-0000-4000-8000-000000000001",
	}
}

func peerResponseFixture(t *testing.T, c *openvpn.Client) service.PeerResponse {
	t.Helper()

	req, err := c.PeerRequest(t.Context())
	require.NoError(t, err)

	var peerReq openvpn.PeerRequest
	require.NoError(t, req.Unpack(&peerReq))

	p := payloadFixture(t)
	p.Username = peerReq.UUID

	resp, err := service.PackResponse(openvpn.Name, service.PeerID(peerReq.UUID), p)
	require.NoError(t, err)

	return resp
}

func requireSignal(t *testing.T, signals <-chan string) {
	t.Helper()

	timer := time.NewTimer(time.Second)
	defer timer.Stop()

	select {
	case signal := <-signals:
		require.Equal(t, "signal SIGTERM", signal)
	case <-timer.C:
		require.FailNow(t, "management SIGTERM not received")
	case <-t.Context().Done():
		require.FailNow(t, "test context ended before management SIGTERM")
	}
}

func startClientManagement(t *testing.T, state string, onSignal func()) (net.Listener, <-chan string) {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	signals := make(chan string, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)

		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go func() {
				defer func() { _ = conn.Close() }()

				line, err := bufio.NewReader(conn).ReadString('\n')
				if err != nil {
					return
				}

				switch strings.TrimSpace(line) {
				case "state":
					_, _ = io.WriteString(conn, "0,"+state+"\nEND\n")
				case "signal SIGTERM":
					signals <- "signal SIGTERM"

					if onSignal != nil {
						onSignal()
					}
				}
			}()
		}
	}()

	t.Cleanup(func() {
		_ = listener.Close()

		<-done
	})

	return listener, signals
}
