package v2raycore_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/internal/v2raycore"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/v2ray"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
	"github.com/sentinel-official/sentinel-go-sdk/v3/xray"
)

func TestInbound_Validate(t *testing.T) {
	t.Parallel()

	type inbound = v2raycore.Inbound[xray.Extensions]

	cases := []struct {
		mutate  func(*inbound)
		name    string
		wantErr bool
	}{
		{
			mutate: func(*inbound) {},
			name:   "vless tls",
		},
		{
			mutate: func(in *inbound) { in.Flow = "xtls-rprx-vision" },
			name:   "vless vision",
		},
		{
			mutate:  func(in *inbound) { in.Port = 0 },
			name:    "port zero",
			wantErr: true,
		},
		{
			mutate:  func(in *inbound) { in.Security = "ssl" },
			name:    "unknown security",
			wantErr: true,
		},
		{
			mutate:  func(in *inbound) { in.Security = "none" },
			name:    "pin without tls",
			wantErr: true,
		},
		{
			mutate:  func(in *inbound) { in.TLSPin = "" },
			name:    "tls without pin",
			wantErr: true,
		},
		{
			mutate:  func(in *inbound) { in.Flow, in.Protocol = "xtls-rprx-vision", "vmess" },
			name:    "flow with vmess",
			wantErr: true,
		},
		{
			mutate:  func(in *inbound) { in.Extensions = extensionsFixture() },
			name:    "reality extensions with tls",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := inboundFixture()
			tc.mutate(&in)

			err := in.Validate()
			if tc.wantErr {
				require.ErrorIs(t, err, service.ErrInvalidPeer)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestInbound_Validate_Injection(t *testing.T) {
	t.Parallel()

	fields := map[string]func(*v2raycore.Inbound[xray.Extensions], string){
		"flow":      func(in *v2raycore.Inbound[xray.Extensions], v string) { in.Flow = v },
		"protocol":  func(in *v2raycore.Inbound[xray.Extensions], v string) { in.Protocol = v },
		"security":  func(in *v2raycore.Inbound[xray.Extensions], v string) { in.Security = v },
		"tls_pin":   func(in *v2raycore.Inbound[xray.Extensions], v string) { in.TLSPin = tlsPin[len(v):] + v },
		"transport": func(in *v2raycore.Inbound[xray.Extensions], v string) { in.Transport = v },
	}

	for field, set := range fields {
		for _, vector := range injectionVectors() {
			t.Run(fmt.Sprintf("%s %q", field, vector), func(t *testing.T) {
				t.Parallel()

				in := inboundFixture()
				set(&in, vector)
				require.ErrorIs(t, in.Validate(), service.ErrInvalidPeer)
			})
		}
	}
}

func TestMetadata_Validate(t *testing.T) {
	t.Parallel()

	md := v2raycore.Metadata[xray.Extensions]{Inbounds: []v2raycore.Inbound[xray.Extensions]{inboundFixture()}}
	require.NoError(t, md.Validate())

	md.Inbounds[0].Transport = "quic"

	err := md.Validate()
	require.ErrorIs(t, err, validate.ErrInvalidConfig)
	require.NotErrorIs(t, err, service.ErrInvalidPeer)
}

func TestPeerRequest_Validate(t *testing.T) {
	t.Parallel()

	require.NoError(t, v2raycore.PeerRequest{UUID: "00000000-0000-4000-8000-000000000001"}.Validate())
	require.ErrorIs(t, v2raycore.PeerRequest{UUID: "not-a-uuid"}.Validate(), service.ErrInvalidPeer)
}

func TestPeerResponse_JSONKeys(t *testing.T) {
	t.Parallel()

	tlsInbound := v2raycore.Inbound[v2ray.Extensions]{
		Port:      443,
		Protocol:  protocolVLESS,
		Security:  v2raycore.SecurityTLS,
		TLSPin:    tlsPin,
		Transport: "ws",
	}
	wantTLSInboundKeys := []string{"port", "protocol", "security", "tls_pin", "transport"}
	require.Equal(t, wantTLSInboundKeys, jsonKeys(t, tlsInbound))

	noneInbound := v2raycore.Inbound[xray.Extensions]{
		Port:      8080,
		Protocol:  "vmess",
		Security:  "none",
		Transport: "tcp",
	}
	wantNoneInboundKeys := []string{"port", "protocol", "security", "transport"}
	require.Equal(t, wantNoneInboundKeys, jsonKeys(t, noneInbound))

	realityInbound := v2raycore.Inbound[xray.Extensions]{
		Extensions: extensionsFixture(),
		Port:       8443,
		Protocol:   protocolVLESS,
		Security:   "reality",
		Transport:  "tcp",
	}
	wantRealityInboundKeys := []string{"extensions", "port", "protocol", "security", "transport"}
	require.Equal(t, wantRealityInboundKeys, jsonKeys(t, realityInbound))

	raw, err := json.Marshal(realityInbound)
	require.NoError(t, err)

	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &decoded))

	var ext map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(decoded["extensions"], &ext))

	wantRealityKeys := []string{"fingerprint", "public_key", "server_name", "short_id"}
	require.Equal(t, wantRealityKeys, jsonKeys(t, ext["reality"]))
	require.NotContains(t, string(raw), "secret")

	md := v2raycore.Metadata[v2ray.Extensions]{Inbounds: []v2raycore.Inbound[v2ray.Extensions]{tlsInbound}}
	wantMdKeys := []string{"inbounds"}
	require.Equal(t, wantMdKeys, jsonKeys(t, md))

	resp := v2raycore.PeerResponse[xray.Extensions]{
		Inbounds: []v2raycore.Inbound[xray.Extensions]{realityInbound},
		UUID:     "u",
	}
	wantRespKeys := []string{"inbounds", "uuid"}
	require.Equal(t, wantRespKeys, jsonKeys(t, resp))

	req := v2raycore.PeerRequest{UUID: "u"}
	wantReqKeys := []string{"uuid"}
	require.Equal(t, wantReqKeys, jsonKeys(t, req))
}

func TestPeerResponse_Validate(t *testing.T) {
	t.Parallel()

	type response = v2raycore.PeerResponse[xray.Extensions]

	cases := []struct {
		mutate  func(*response)
		name    string
		wantErr bool
	}{
		{
			mutate: func(*response) {},
			name:   "valid",
		},
		{
			mutate:  func(r *response) { r.Inbounds = nil },
			name:    "no inbounds",
			wantErr: true,
		},
		{
			mutate:  func(r *response) { r.Inbounds = slices.Repeat(r.Inbounds, 17) },
			name:    "too many inbounds",
			wantErr: true,
		},
		{
			mutate:  func(r *response) { r.Inbounds[0].Port = 0 },
			name:    "invalid inbound",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resp := peerResponseFixture()
			tc.mutate(&resp)

			err := resp.Validate()
			if tc.wantErr {
				require.ErrorIs(t, err, service.ErrInvalidPeer)

				return
			}

			require.NoError(t, err)
		})
	}

	for _, vector := range injectionVectors() {
		t.Run(fmt.Sprintf("uuid %q", vector), func(t *testing.T) {
			t.Parallel()

			resp := peerResponseFixture()
			resp.UUID += vector
			require.ErrorIs(t, resp.Validate(), service.ErrInvalidPeer)
		})
	}
}

func injectionVectors() []string {
	return []string{
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
}

func peerResponseFixture() v2raycore.PeerResponse[xray.Extensions] {
	return v2raycore.PeerResponse[xray.Extensions]{
		Inbounds: []v2raycore.Inbound[xray.Extensions]{inboundFixture()},
		UUID:     "00000000-0000-4000-8000-000000000001",
	}
}
