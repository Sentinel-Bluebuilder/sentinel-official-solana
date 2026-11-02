package hysteria2_test

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/sentinel-official/sentinel-go-sdk/v3/hysteria2"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
)

func TestClient_Cleanup_RemovesToolCopies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		content string
		name    string
		removed bool
	}{
		{content: "# " + service.ManagedMarker + "\nconfig", name: "owned", removed: true},
		{content: "config", name: "foreign", removed: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := service.Placement{Dir: t.TempDir(), Interface: "dvpn-c0", ToolDir: t.TempDir()}
			require.NoError(t, os.WriteFile(p.ToolPath(".yaml"), []byte(tc.content), 0o600))

			c, err := hysteria2.NewClient(hysteria2.DefaultClientConfig(), p, zerolog.Nop())
			require.NoError(t, err)
			require.NoError(t, c.(service.ClientCleaner).Cleanup(t.Context()))

			_, err = os.Stat(p.ToolPath(".yaml"))
			require.Equal(t, tc.removed, errors.Is(err, os.ErrNotExist))
		})
	}
}

func TestClient_Start_FailedReadinessStopsProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a shell")
	}

	dir := t.TempDir()
	marker := filepath.Join(dir, "stopped")
	bin := filepath.Join(dir, hysteria2.Binary)
	script := "#!/bin/sh\ntrap 'sleep 0.2; printf stopped > \"$HY2_MARKER\"; exit 0' TERM\nwhile :; do sleep 1; done\n"
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o700)) //#nosec G306
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HY2_MARKER", marker)

	p := service.Placement{Dir: dir, Interface: "hy2-missing0", ToolDir: t.TempDir()}

	client, err := hysteria2.NewClient(hysteria2.DefaultClientConfig(), p, zerolog.Nop())
	require.NoError(t, err)

	id := requestUUID(t, client)
	resp, err := service.PackResponse(hysteria2.Name, service.PeerID(id), hysteria2.PeerResponse{
		Auth:   id,
		Port:   443,
		TLSPin: strings.TrimSuffix(strings.Repeat("AB:", 32), ":"),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	err = client.Start(ctx, "203.0.113.1", resp)
	require.Error(t, err)
	require.True(t, errors.Is(err, context.DeadlineExceeded) || errors.Is(err, service.ErrProcessNotReady))
	require.NoFileExists(t, p.ToolPath(".yaml"))

	raw, err := os.ReadFile(marker) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, "stopped", string(raw))
}

func TestClient_Start_RejectsForeignAuth(t *testing.T) {
	t.Parallel()

	foreign := "00000000-0000-4000-8000-000000000002"
	cases := []struct {
		auth    string
		id      service.PeerID
		name    string
		service service.Name
	}{
		{
			auth:    foreign,
			name:    "foreign auth",
			service: hysteria2.Name,
		},
		{
			id:      service.PeerID(foreign),
			name:    "foreign id",
			service: hysteria2.Name,
		},
		{
			name:    "foreign service",
			service: "v2ray",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, err := hysteria2.NewClient(
				hysteria2.DefaultClientConfig(), service.Placement{Dir: t.TempDir(), Interface: "dvpn-c0"}, zerolog.Nop(),
			)
			require.NoError(t, err)

			id := requestUUID(t, c)
			resp, err := service.PackResponse(tc.service, cmp.Or(tc.id, service.PeerID(id)), hysteria2.PeerResponse{
				Auth:   cmp.Or(tc.auth, id),
				Port:   443,
				TLSPin: strings.TrimSuffix(strings.Repeat("AB:", 32), ":"),
			})
			require.NoError(t, err)

			require.ErrorIs(t, c.Start(t.Context(), "203.0.113.1", resp), service.ErrInvalidPeer)
		})
	}
}

func TestClient_Stats_NotStarted(t *testing.T) {
	t.Parallel()

	c, err := hysteria2.NewClient(
		hysteria2.DefaultClientConfig(), service.Placement{Dir: t.TempDir(), Interface: "dvpn-c0"}, zerolog.Nop(),
	)
	require.NoError(t, err)

	if runtime.GOOS != "darwin" {
		require.Equal(t, "dvpn-c0", c.Interface())
	}

	_, err = c.Stats(t.Context())
	require.ErrorIs(t, err, service.ErrServiceNotStarted)
}

func TestNewClient_InvalidInterface(t *testing.T) {
	t.Parallel()

	_, err := hysteria2.NewClient(hysteria2.ClientConfig{}, service.Placement{Dir: t.TempDir()}, zerolog.Nop())
	require.ErrorIs(t, err, validate.ErrInvalidConfig)
}

func TestWriteConf(t *testing.T) {
	t.Parallel()

	resp := hysteria2.PeerResponse{
		Auth:   "00000000-0000-4000-8000-000000000001",
		Port:   443,
		TLSPin: strings.TrimSuffix(strings.Repeat("AB:", 32), ":"),
	}
	withObfs := resp
	withObfs.ObfsPassword = "pw"
	cases := []struct {
		addr   netip.Addr
		golden string
		iface  string
		keys   []string
		name   string
		resp   hysteria2.PeerResponse
	}{
		{
			addr:   netip.MustParseAddr("203.0.113.1"),
			golden: "client_ipv4.yaml",
			iface:  "dvpn-c0",
			keys:   []string{"auth", "obfs", "server", "tls", "tun"},
			name:   "ipv4",
			resp:   withObfs,
		},
		{
			addr:   netip.MustParseAddr("2001:db8::1"),
			golden: "client_ipv6_noiface.yaml",
			keys:   []string{"auth", "server", "tls", "tun"},
			name:   "ipv6 without interface",
			resp:   resp,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "client.yaml")
			require.NoError(t, hysteria2.WriteConf(path, tc.addr, tc.iface, tc.resp))

			raw, err := os.ReadFile(path) //#nosec G304
			require.NoError(t, err)

			want, err := os.ReadFile(filepath.Join("testdata", tc.golden)) //#nosec G304
			require.NoError(t, err)
			require.Equal(t, string(want), string(raw))

			var doc map[string]any
			require.NoError(t, yaml.Unmarshal(raw, &doc))
			require.ElementsMatch(t, tc.keys, slices.Collect(maps.Keys(doc)))
		})
	}
}

func TestWriteConf_DigitPassword(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "client.yaml")
	resp := hysteria2.PeerResponse{
		Auth:         "00000000-0000-4000-8000-000000000001",
		ObfsPassword: "12345",
		Port:         443,
		TLSPin:       strings.TrimSuffix(strings.Repeat("AB:", 32), ":"),
	}
	require.NoError(t, hysteria2.WriteConf(path, netip.MustParseAddr("203.0.113.1"), "", resp))

	raw, err := os.ReadFile(path) //#nosec G304
	require.NoError(t, err)

	var doc struct {
		Obfs struct {
			Salamander struct {
				Password any `yaml:"password"`
			} `yaml:"salamander"`
		} `yaml:"obfs"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	require.Equal(t, "12345", doc.Obfs.Salamander.Password)
}

func requestUUID(t *testing.T, c service.Client) string {
	t.Helper()

	req, err := c.PeerRequest(t.Context())
	require.NoError(t, err)

	var sent hysteria2.PeerRequest
	require.NoError(t, req.Unpack(&sent))

	return sent.UUID
}
