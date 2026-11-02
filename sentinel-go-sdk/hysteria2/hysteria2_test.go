package hysteria2_test

import (
	"encoding/json"
	"maps"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/hysteria2"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
)

func TestMetadata_Validate(t *testing.T) {
	t.Parallel()

	md := hysteria2.Metadata{
		ObfsPassword: "pw",
		Port:         443,
		TLSPin:       strings.TrimSuffix(strings.Repeat("AB:", 32), ":"),
	}
	require.NoError(t, md.Validate())

	md.ObfsPassword = "pw\nsocks5:"
	require.ErrorIs(t, md.Validate(), validate.ErrInvalidConfig)
}

func TestNewClient_Registered(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"hysteria2"}, service.Binaries(hysteria2.Name))

	if runtime.GOOS != "linux" {
		_, err := service.DefaultServerConfig(hysteria2.Name)
		require.ErrorIs(t, err, service.ErrUnknownName)
	}

	sdktest.RequireClientRegistered(t, hysteria2.Name, 1<<5, hysteria2.ClientConfig{})

	dir, err := service.ClientToolDir(hysteria2.Name)
	require.NoError(t, err)
	require.Equal(t, "/etc/hysteria/sentinel-dvpn", dir)
}

func TestPeerRequest_Validate(t *testing.T) {
	t.Parallel()

	id := "00000000-0000-4000-8000-000000000001"
	payload := hysteria2.PeerRequest{UUID: id}

	req, err := service.PackRequest(hysteria2.Name, payload)
	require.NoError(t, err)

	raw, err := json.Marshal(req)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"hysteria2","payload":{"uuid":"`+id+`"}}`, string(raw))

	var p hysteria2.PeerRequest
	require.NoError(t, req.Unpack(&p))
	require.NoError(t, p.Validate())

	p.UUID = "not-a-uuid"
	require.ErrorIs(t, p.Validate(), service.ErrInvalidPeer)
}

func TestPeerResponse_Validate(t *testing.T) {
	t.Parallel()

	type validateCase struct {
		field string
		name  string
		value any
	}

	valid := map[string]any{
		"auth":          "00000000-0000-4000-8000-000000000001",
		"obfs_password": "pw",
		"port":          443,
		"tls_pin":       strings.TrimSuffix(strings.Repeat("AB:", 32), ":"),
	}
	cases := []validateCase{
		{
			field: "obfs_password",
			name:  "obfs password with space",
			value: "p w",
		},
		{
			field: "obfs_password",
			name:  "obfs password too long",
			value: strings.Repeat("a", 65),
		},
		{
			field: "obfs_password",
			name:  "obfs password with quote",
			value: `pw"`,
		},
		{
			field: "obfs_password",
			name:  "obfs password with backslash",
			value: `pw\`,
		},
		{
			field: "tls_pin",
			name:  "tls pin lowercase",
			value: strings.Repeat("ab:", 31) + "ab",
		},
		{
			field: "tls_pin",
			name:  "tls pin short",
			value: strings.Repeat("AB:", 30) + "AB",
		},
		{
			field: "port",
			name:  "port zero",
			value: 0,
		},
	}
	controls := []string{
		"\n", "\r\n", "\x00", "\n[Interface]\nPostUp = id\n", "\nscript-security 2\nup /bin/sh",
		"\nsocks5:\n  listen: 0.0.0.0:1080",
	}
	printables := []string{`"`, "'", "]", `"},{"listen":"0.0.0.0"`}
	vectors := map[string][]string{
		"auth":          append(controls, printables...),
		"obfs_password": controls,
		"tls_pin":       append(controls, printables...),
	}

	for field, values := range vectors {
		for _, vector := range values {
			value := valid[field].(string) + vector //nolint:forcetypeassert
			cases = append(cases, validateCase{
				field: field,
				name:  field + " " + vector,
				value: value,
			})
		}
	}

	unpack := func(payload map[string]any) error {
		resp, err := service.PackResponse(hysteria2.Name, "peer", payload)
		require.NoError(t, err)

		_, err = service.UnpackResponse[hysteria2.PeerResponse](resp)

		return err
	}

	require.NoError(t, unpack(valid))

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			payload := maps.Clone(valid)
			payload[tc.field] = tc.value
			require.ErrorIs(t, unpack(payload), service.ErrInvalidPeer)
		})
	}
}

func TestServerConfig_Validate(t *testing.T) {
	t.Parallel()

	cfg := hysteria2.DefaultServerConfig()
	cfg.AuthPort = cfg.ListenPort
	require.ErrorContains(t, cfg.Validate(), "listen_port nefield=AuthPort")

	cfg = hysteria2.DefaultServerConfig()
	cfg.StatsPort = cfg.ListenPort
	require.ErrorContains(t, cfg.Validate(), "listen_port nefield=StatsPort")

	cfg = hysteria2.DefaultServerConfig()
	cfg.StatsPort = cfg.AuthPort
	require.ErrorContains(t, cfg.Validate(), "auth_port nefield=StatsPort")

	cfg = hysteria2.DefaultServerConfig()
	cfg.StatsSecret = ""
	require.ErrorIs(t, cfg.Validate(), validate.ErrInvalidConfig)
}

func TestServerConfig_Validate_ObfsPassword(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		password string
	}{
		{name: "space", password: "p w"},
		{name: "quote", password: `pw"`},
		{name: "backslash", password: `pw\`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := hysteria2.DefaultServerConfig()
			cfg.ObfsPassword = tc.password
			require.ErrorIs(t, cfg.Validate(), validate.ErrInvalidConfig)
		})
	}
}
