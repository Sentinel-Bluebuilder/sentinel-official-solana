package openvpn_test

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/openvpn"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
)

func TestMetadata_Validate(t *testing.T) {
	t.Parallel()

	valid := metadataFixture(t)
	lines := strings.SplitAfter(valid.TLSCrypt, "\n")
	bare := strings.Join(lines[3:], "")

	cases := []struct {
		edit func(m *openvpn.Metadata)
		name string
		ok   bool
	}{
		{
			edit: func(*openvpn.Metadata) {},
			name: "valid",
			ok:   true,
		},
		{
			edit: func(m *openvpn.Metadata) { m.CA = "extra\n" + m.CA },
			name: "ca leading bytes",
		},
		{
			edit: func(m *openvpn.Metadata) { m.CA += "extra" },
			name: "ca trailing bytes",
		},
		{
			edit: func(m *openvpn.Metadata) { m.CA += m.CA },
			name: "ca two blocks",
		},
		{
			edit: func(m *openvpn.Metadata) { m.Port = 0 },
			name: "port",
		},
		{
			edit: func(m *openvpn.Metadata) { m.Protocol = "udp4" },
			name: "protocol",
		},
		{
			edit: func(m *openvpn.Metadata) { m.TLSCrypt = bare },
			name: "tls-crypt without comments",
			ok:   true,
		},
		{
			edit: func(m *openvpn.Metadata) { m.TLSCrypt = strings.Repeat("#\n", 9) + bare },
			name: "tls-crypt 9 comments",
		},
		{
			edit: func(m *openvpn.Metadata) { m.TLSCrypt = "#" + strings.Repeat("x", 120) + "\n" + bare },
			name: "tls-crypt 121-byte comment",
		},
		{
			edit: func(m *openvpn.Metadata) { m.TLSCrypt = strings.Replace(m.TLSCrypt, lines[4], lines[4][1:], 1) },
			name: "tls-crypt 31 hex",
		},
		{
			edit: func(m *openvpn.Metadata) { m.TLSCrypt = strings.ReplaceAll(m.TLSCrypt, "\n", "\r\n") },
			name: "tls-crypt crlf",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := valid
			tc.edit(&m)

			if tc.ok {
				require.NoError(t, m.Validate())
			} else {
				require.ErrorIs(t, m.Validate(), validate.ErrInvalidConfig)
			}
		})
	}
}

func TestNewClient_Registered(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"openvpn"}, service.Binaries(openvpn.Name))

	if runtime.GOOS != "linux" {
		_, err := service.DefaultServerConfig(openvpn.Name)
		require.ErrorIs(t, err, service.ErrUnknownName)
	}

	sdktest.RequireClientRegistered(t, openvpn.Name, 1<<2, openvpn.ClientConfig{})

	dir, err := service.ClientToolDir(openvpn.Name)
	require.NoError(t, err)
	require.Equal(t, "/etc/openvpn/client/sentinel-dvpn", dir)
}

func TestNewTLSCryptKey(t *testing.T) {
	t.Parallel()

	key, err := openvpn.NewTLSCryptKey()
	require.NoError(t, err)
	require.NoError(t, openvpn.ValidateTLSCrypt(string(key)))

	lines := strings.Split(strings.TrimSuffix(string(key), "\n"), "\n")
	require.Len(t, lines, 18)
	require.Equal(t, "-----BEGIN OpenVPN Static key V1-----", lines[0])

	other, err := openvpn.NewTLSCryptKey()
	require.NoError(t, err)
	require.NotEqual(t, key, other)
}

func TestPeerRequest_Validate(t *testing.T) {
	t.Parallel()

	id := "00000000-0000-4000-8000-000000000001"
	payload := openvpn.PeerRequest{UUID: id}

	req, err := service.PackRequest(openvpn.Name, payload)
	require.NoError(t, err)

	raw, err := json.Marshal(req)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"openvpn","payload":{"uuid":"`+id+`"}}`, string(raw))

	var p openvpn.PeerRequest
	require.NoError(t, req.Unpack(&p))
	require.NoError(t, p.Validate())

	p = openvpn.PeerRequest{UUID: "not-a-uuid"}
	require.ErrorIs(t, p.Validate(), service.ErrInvalidPeer)
}

func TestPeerResponse_Validate(t *testing.T) {
	t.Parallel()

	type testCase struct {
		edit func(p *openvpn.PeerResponse)
		name string
		ok   bool
	}

	cases := []testCase{
		{
			edit: func(*openvpn.PeerResponse) {},
			name: "valid",
			ok:   true,
		},
		{
			edit: func(p *openvpn.PeerResponse) { p.CA += "extra" },
			name: "ca",
		},
		{
			edit: func(p *openvpn.PeerResponse) { p.Password = "secretpass" },
			name: "password",
		},
		{
			edit: func(p *openvpn.PeerResponse) { p.Username = "user-1" },
			name: "username",
		},
	}

	vectors := []string{
		"\n",
		"\r\n",
		"\x00",
		`"`,
		"'",
		"]",
		"\n[Interface]\nPostUp = id\n",
		"\nscript-security 2\nup /bin/sh",
		"\nsocks5:\n  listen: 0.0.0.0:1080",
		`"},{"listen":"0.0.0.0"`,
	}

	for _, vector := range vectors {
		for field, edit := range map[string]func(p *openvpn.PeerResponse){
			"password": func(p *openvpn.PeerResponse) { p.Password += vector },
			"protocol": func(p *openvpn.PeerResponse) { p.Protocol += vector },
			"username": func(p *openvpn.PeerResponse) { p.Username += vector },
		} {
			cases = append(cases, testCase{
				edit: edit,
				name: fmt.Sprintf("%s %q", field, vector),
			})
		}
	}

	valid := payloadFixture(t)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := valid
			tc.edit(&p)

			if tc.ok {
				require.NoError(t, p.Validate())
			} else {
				require.ErrorIs(t, p.Validate(), service.ErrInvalidPeer)
			}
		})
	}
}

func TestServerConfig_Validate(t *testing.T) {
	t.Parallel()

	cfg := openvpn.DefaultServerConfig()
	cfg.Addresses = []string{"10.0.0.1"}
	require.ErrorIs(t, cfg.Validate(), validate.ErrInvalidConfig)

	cfg = openvpn.DefaultServerConfig()
	cfg.Addresses = []string{"10.0.0.1/024"}
	err := cfg.Validate()
	require.ErrorIs(t, err, validate.ErrInvalidConfig)
	require.EqualError(t, err, `invalid configuration: address "10.0.0.1/024"`)

	cfg = openvpn.DefaultServerConfig()
	cfg.Protocol = "quic"
	require.ErrorContains(t, cfg.Validate(), "protocol oneof=udp tcp")

	cfg = openvpn.DefaultServerConfig()
	cfg.ManagementPort = cfg.ListenPort
	require.ErrorContains(t, cfg.Validate(), "listen_port nefield=ManagementPort")

	cfg = openvpn.DefaultServerConfig()
	cfg.EgressInterface = "0123456789abcdef"
	require.ErrorIs(t, cfg.Validate(), validate.ErrInvalidConfig)

	cfg = openvpn.DefaultServerConfig()
	cfg.EgressInterface = ""
	require.NoError(t, cfg.Validate())
}
