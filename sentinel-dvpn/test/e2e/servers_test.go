//go:build e2e && integration

package e2e_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netns"

	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
)

const (
	dnsProbeActiveTimeout = 5 * time.Second
	dnsProbeAddr          = "198.51.100.5:53"
	dnsProbePayload       = "e2e-dns-probe"
	httpProbeBody         = "e2e-internet-probe"
	internetHTTPAddr      = "198.51.100.5:8080"
	realityDestAddr       = "198.51.100.5:443"
	realityServerName     = "e2e-reality.internal"
	serverHeaderTimeout   = 5 * time.Second
)

const (
	socksATYPIPv4         = 1
	socksATYPIPv6         = 4
	socksCmdUDPAssociate  = 3
	socksMaxDatagramBytes = 1500
	socksNoAuth           = 0
	socksSucceeded        = 0
	socksVersion          = 5
)

var errSOCKSExchange = errors.New("socks5 exchange failed")

func (ns *namespace) dial(t *testing.T, ctx context.Context, network, addr string) net.Conn {
	t.Helper()

	runtime.LockOSThread()

	defer runtime.UnlockOSThread()

	origin, err := netns.Get()
	require.NoError(t, err)

	closeOrigin := func() { _ = origin.Close() }
	defer closeOrigin()

	restoreOrigin := func() { _ = netns.Set(origin) }
	defer restoreOrigin()

	require.NoError(t, netns.Set(ns.handle))

	var dialer net.Dialer

	conn, err := dialer.DialContext(ctx, network, addr)
	require.NoError(t, err)

	return conn
}

func (ns *namespace) listen(t *testing.T, ctx context.Context, network, addr string) net.Listener {
	t.Helper()

	runtime.LockOSThread()

	defer runtime.UnlockOSThread()

	origin, err := netns.Get()
	require.NoError(t, err)

	closeOrigin := func() { _ = origin.Close() }
	defer closeOrigin()

	restoreOrigin := func() { _ = netns.Set(origin) }
	defer restoreOrigin()

	require.NoError(t, netns.Set(ns.handle))

	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, network, addr)
	require.NoError(t, err)

	return ln
}

func (ns *namespace) listenPacket(t *testing.T, ctx context.Context, network, addr string) net.PacketConn {
	t.Helper()

	runtime.LockOSThread()

	defer runtime.UnlockOSThread()

	origin, err := netns.Get()
	require.NoError(t, err)

	closeOrigin := func() { _ = origin.Close() }
	defer closeOrigin()

	restoreOrigin := func() { _ = netns.Set(origin) }
	defer restoreOrigin()

	require.NoError(t, netns.Set(ns.handle))

	var lc net.ListenConfig

	pc, err := lc.ListenPacket(ctx, network, addr)
	require.NoError(t, err)

	return pc
}

func associateSOCKS(control io.ReadWriter, proxy netip.AddrPort) (netip.AddrPort, error) {
	if _, err := control.Write([]byte{socksVersion, 1, socksNoAuth}); err != nil {
		return netip.AddrPort{}, fmt.Errorf("writing socks greeting: %w", err)
	}

	var method [2]byte
	if _, err := io.ReadFull(control, method[:]); err != nil {
		return netip.AddrPort{}, fmt.Errorf("reading socks method: %w", err)
	}

	if method != [2]byte{socksVersion, socksNoAuth} {
		return netip.AddrPort{}, fmt.Errorf("%w: method reply %v", errSOCKSExchange, method)
	}

	req := append(
		[]byte{socksVersion, socksCmdUDPAssociate, 0},
		encodeSOCKSAddr(netip.AddrPortFrom(netip.IPv4Unspecified(), 0))...,
	)
	if _, err := control.Write(req); err != nil {
		return netip.AddrPort{}, fmt.Errorf("writing socks udp associate: %w", err)
	}

	var head [3]byte
	if _, err := io.ReadFull(control, head[:]); err != nil {
		return netip.AddrPort{}, fmt.Errorf("reading socks udp associate reply: %w", err)
	}

	if head[0] != socksVersion || head[1] != socksSucceeded {
		return netip.AddrPort{}, fmt.Errorf("%w: udp associate reply %v", errSOCKSExchange, head)
	}

	relay, err := readSOCKSAddr(control)
	if err != nil {
		return netip.AddrPort{}, err
	}

	if relay.Addr().IsUnspecified() {
		relay = netip.AddrPortFrom(proxy.Addr(), relay.Port())
	}

	return relay, nil
}

func curlArgs(proxy string) []string {
	args := []string{"--max-time", "5", "-s"}
	if proxy != "" {
		args = append(args, "--socks5", proxy)
	}

	return append(args, "http://"+internetHTTPAddr+"/")
}

func decodeSOCKSDatagram(datagram []byte) (netip.AddrPort, []byte, error) {
	if len(datagram) < 3 || datagram[2] != 0 {
		return netip.AddrPort{}, nil, fmt.Errorf("%w: datagram header %v", errSOCKSExchange, datagram)
	}

	reader := bytes.NewReader(datagram[3:])

	src, err := readSOCKSAddr(reader)
	if err != nil {
		return netip.AddrPort{}, nil, err
	}

	return src, datagram[len(datagram)-reader.Len():], nil
}

func echoSOCKSUDP(
	control net.Conn,
	proxy netip.AddrPort,
	dialUDP func(addr string) net.Conn,
	dst netip.AddrPort,
	payload []byte,
	deadline time.Time,
) error {
	if err := control.SetDeadline(deadline); err != nil {
		return fmt.Errorf("setting socks control deadline: %w", err)
	}

	relay, err := associateSOCKS(control, proxy)
	if err != nil {
		return err
	}

	conn := dialUDP(relay.String())

	closeConn := func() { _ = conn.Close() }
	defer closeConn()

	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("setting socks relay deadline: %w", err)
	}

	if _, err := conn.Write(encodeSOCKSDatagram(dst, payload)); err != nil {
		return fmt.Errorf("writing socks datagram: %w", err)
	}

	buf := make([]byte, socksMaxDatagramBytes)

	size, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("reading socks datagram: %w", err)
	}

	src, data, err := decodeSOCKSDatagram(buf[:size])
	if err != nil {
		return err
	}

	if src != dst || !bytes.Equal(data, payload) {
		return fmt.Errorf("%w: echo from %s carried %q", errSOCKSExchange, src, data)
	}

	return nil
}

func encodeSOCKSAddr(addrPort netip.AddrPort) []byte {
	atyp := byte(socksATYPIPv4)
	if addrPort.Addr().Is6() {
		atyp = socksATYPIPv6
	}

	encoded := append([]byte{atyp}, addrPort.Addr().AsSlice()...)

	return binary.BigEndian.AppendUint16(encoded, addrPort.Port())
}

func encodeSOCKSDatagram(dst netip.AddrPort, payload []byte) []byte {
	datagram := append([]byte{0, 0, 0}, encodeSOCKSAddr(dst)...)

	return append(datagram, payload...)
}

func probeDNS(t *testing.T, ctx context.Context, ns *namespace, proxy string, timeout time.Duration) error {
	t.Helper()

	deadline := time.Now().Add(timeout)
	payload := []byte(dnsProbePayload)

	if proxy != "" {
		control := ns.dial(t, ctx, "tcp", proxy)

		closeControl := func() { _ = control.Close() }
		defer closeControl()

		dialUDP := func(addr string) net.Conn { return ns.dial(t, ctx, "udp", addr) }

		return echoSOCKSUDP(
			control,
			netip.MustParseAddrPort(proxy),
			dialUDP,
			netip.MustParseAddrPort(dnsProbeAddr),
			payload,
			deadline,
		)
	}

	conn := ns.dial(t, ctx, "udp", dnsProbeAddr)

	closeConn := func() { _ = conn.Close() }
	defer closeConn()

	require.NoError(t, conn.SetDeadline(deadline))

	if _, err := conn.Write(payload); err != nil {
		return fmt.Errorf("writing dns probe: %w", err)
	}

	resp := make([]byte, len(payload))
	if _, err := conn.Read(resp); err != nil {
		return fmt.Errorf("reading dns probe response: %w", err)
	}

	return nil
}

func readSOCKSAddr(reader io.Reader) (netip.AddrPort, error) {
	var atyp [1]byte
	if _, err := io.ReadFull(reader, atyp[:]); err != nil {
		return netip.AddrPort{}, fmt.Errorf("reading socks address type: %w", err)
	}

	size := net.IPv4len

	switch atyp[0] {
	case socksATYPIPv4:
	case socksATYPIPv6:
		size = net.IPv6len
	default:
		return netip.AddrPort{}, fmt.Errorf("%w: address type %d", errSOCKSExchange, atyp[0])
	}

	buf := make([]byte, size+2)
	if _, err := io.ReadFull(reader, buf); err != nil {
		return netip.AddrPort{}, fmt.Errorf("reading socks address: %w", err)
	}

	addr, _ := netip.AddrFromSlice(buf[:size])

	return netip.AddrPortFrom(addr, binary.BigEndian.Uint16(buf[size:])), nil
}

func startDNSEchoServer(t *testing.T, ctx context.Context, tp *topology) {
	t.Helper()

	pc := tp.internet.listenPacket(t, ctx, "udp", dnsProbeAddr)

	buf := make([]byte, len(dnsProbePayload))

	serve := func() {
		for {
			size, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}

			_, _ = pc.WriteTo(buf[:size], addr)
		}
	}
	go serve()

	closeServer := func() { _ = pc.Close() }
	t.Cleanup(closeServer)
}

func startInternetServer(t *testing.T, ctx context.Context, tp *topology) {
	t.Helper()

	ln := tp.internet.listen(t, ctx, "tcp", internetHTTPAddr)

	mux := http.NewServeMux()
	handler := func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(httpProbeBody))
	}
	mux.HandleFunc("/", handler)

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: serverHeaderTimeout,
	}

	serve := func() { _ = srv.Serve(ln) }
	go serve()

	closeServer := func() { _ = srv.Close() }
	t.Cleanup(closeServer)
}

func startRealityDestServer(t *testing.T, ctx context.Context, tp *topology) {
	t.Helper()

	cert, _, err := api.LoadOrCreateServerCert(t.TempDir())
	require.NoError(t, err)

	ln := tp.internet.listen(t, ctx, "tcp", realityDestAddr)

	mux := http.NewServeMux()
	handler := func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(httpProbeBody))
	}
	mux.HandleFunc("/", handler)

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: serverHeaderTimeout,
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2", "http/1.1"}},
	}

	serve := func() { _ = srv.ServeTLS(ln, "", "") }
	go serve()

	closeServer := func() { _ = srv.Close() }
	t.Cleanup(closeServer)
}
