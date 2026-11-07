package wgcore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/internal/wgcore"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

const fakeName = "wgtest"

type extensions struct {
	Obfuscation obfuscation `json:"obfuscation"`
}

func (extensions) Validate() error {
	return nil
}

type fakeRunner struct {
	down          []string
	downErr       error
	downRemaining []time.Duration
	handshakes    map[string]time.Time
	handshakeErr  error
	handshakeHits int
	live          string
	queried       []string
	removed       []string
	set           []string
	setErr        error
	transfer      map[string]service.PeerStats
	transferErr   error
	up            []string
	upErr         error
}

func (f *fakeRunner) Cleanup(_ context.Context, _ string, _ bool) error {
	return nil
}

func (f *fakeRunner) Down(ctx context.Context, conf string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("fake down: %w", err)
	}

	if f.downErr != nil {
		return f.downErr
	}

	f.down = append(f.down, conf)

	deadline, ok := ctx.Deadline()
	if ok {
		f.downRemaining = append(f.downRemaining, time.Until(deadline))
	} else {
		f.downRemaining = append(f.downRemaining, 0)
	}

	return nil
}

func (f *fakeRunner) LatestHandshakes(_ context.Context, _ string) (map[string]time.Time, error) {
	f.handshakeHits++
	if f.handshakeErr != nil {
		return nil, f.handshakeErr
	}

	return maps.Clone(f.handshakes), nil
}

func (f *fakeRunner) RemovePeer(_ context.Context, iface, publicKey string) error {
	f.removed = append(f.removed, iface+" "+publicKey)

	return nil
}

func (f *fakeRunner) SetPeer(_ context.Context, iface, publicKey string, allowedIPs []netip.Prefix) error {
	if f.setErr != nil {
		return f.setErr
	}

	allowed := make([]string, 0, len(allowedIPs))
	for _, p := range allowedIPs {
		allowed = append(allowed, p.String())
	}

	f.set = append(f.set, iface+" "+publicKey+" "+strings.Join(allowed, ","))

	return nil
}

func (f *fakeRunner) Transfer(_ context.Context, iface string) (map[string]service.PeerStats, error) {
	f.queried = append(f.queried, iface)
	if f.transferErr != nil {
		return nil, f.transferErr
	}

	return f.transfer, nil
}

func (f *fakeRunner) Up(_ context.Context, conf string) (string, error) {
	if f.upErr != nil {
		return "", f.upErr
	}

	f.up = append(f.up, conf)

	if f.live != "" {
		return f.live, nil
	}

	return strings.TrimSuffix(filepath.Base(conf), ".conf"), nil
}

type obfuscation struct {
	H1   uint32 `json:"h1"`
	H2   uint32 `json:"h2"`
	H3   uint32 `json:"h3"`
	H4   uint32 `json:"h4"`
	I1   string `json:"i1,omitempty"`
	I2   string `json:"i2,omitempty"`
	I3   string `json:"i3,omitempty"`
	I4   string `json:"i4,omitempty"`
	I5   string `json:"i5,omitempty"`
	Jc   uint32 `json:"jc"`
	Jmax uint32 `json:"jmax"`
	Jmin uint32 `json:"jmin"`
	S1   uint32 `json:"s1"`
	S2   uint32 `json:"s2"`
	S3   uint32 `json:"s3"`
	S4   uint32 `json:"s4"`
}

type plainExtensions struct{}

func (plainExtensions) Validate() error {
	return nil
}

func jsonKeys(t *testing.T, v any) []string {
	t.Helper()

	raw, err := json.Marshal(v)
	require.NoError(t, err)

	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &m))

	return slices.Sorted(maps.Keys(m))
}

func mustPublicKey(t *testing.T) string {
	t.Helper()

	private, err := wgcore.NewPrivateKey()
	require.NoError(t, err)

	public, err := wgcore.DerivePublicKey(private)
	require.NoError(t, err)

	return public
}

func obfuscationFixture() obfuscation {
	return obfuscation{
		H1:   5,
		H2:   6,
		H3:   7,
		H4:   8,
		Jc:   3,
		Jmax: 100,
		Jmin: 64,
		S1:   10,
		S2:   20,
		S3:   30,
		S4:   4,
	}
}

func peerPayloadFixture(t *testing.T) wgcore.PeerResponse[plainExtensions] {
	t.Helper()

	return wgcore.PeerResponse[plainExtensions]{
		Addresses: []string{"10.0.0.2/32"},
		MTU:       1420,
		Port:      12345,
		PublicKey: mustPublicKey(t),
	}
}

func peerResponseFixture(t *testing.T, id string) service.PeerResponse {
	t.Helper()

	resp, err := service.PackResponse(service.Name(fakeName), service.PeerID(id), peerPayloadFixture(t))
	require.NoError(t, err)

	return resp
}

func plainVariantFixture() wgcore.Variant {
	return wgcore.Variant{
		Binary:       "wg",
		Name:         service.Name(fakeName),
		Templates:    os.DirFS("../../wireguard"),
		TunnelBinary: "wg-quick",
	}
}

func requireClientConf(t *testing.T, raw, privateKey, serverKey, endpoint string) {
	t.Helper()

	require.Contains(t, raw, "Address = 10.0.0.2/32\n")
	require.Contains(t, raw, "PrivateKey = "+privateKey+"\n")
	require.Contains(t, raw, "MTU = 1420\n")
	require.Contains(t, raw, "PublicKey = "+serverKey+"\n")
	require.Contains(t, raw, "Endpoint = "+endpoint+"\n")
	require.Contains(t, raw, "AllowedIPs = "+wgcore.AllowedIPs(runtime.GOOS)+"\n")
	require.Contains(t, raw, "PersistentKeepalive = 25\n")
	require.NotContains(t, raw, "DNS")
}

func wireType(t *testing.T, v any) string {
	t.Helper()

	raw, err := json.Marshal(v)
	require.NoError(t, err)

	var envelope struct {
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(raw, &envelope))

	return envelope.Type
}
