package wgcore_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/internal/wgcore"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
)

var errFailingExtensions = errors.New("bad extensions")

//nolint:gochecknoglobals
var injectionVectors = []string{
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

type failingExtensions struct{}

func (failingExtensions) Validate() error {
	return errFailingExtensions
}

func TestDerivePublicKey(t *testing.T) {
	t.Parallel()

	private, err := wgcore.NewPrivateKey()
	require.NoError(t, err)
	require.Len(t, private, 44)

	public, err := wgcore.DerivePublicKey(private)
	require.NoError(t, err)
	require.Len(t, public, 44)
	require.NotEqual(t, private, public)

	raw, err := wgcore.DecodeKey(public)
	require.NoError(t, err)
	require.Len(t, raw, 32)

	_, err = wgcore.DecodeKey("nope")
	require.Error(t, err)

	_, err = wgcore.DecodeKey("AAAA")
	require.ErrorContains(t, err, "key is 3 bytes, want 32")

	_, err = wgcore.DecodeKey(public + "\n")
	require.ErrorContains(t, err, "key is not canonical base64")

	_, err = wgcore.DerivePublicKey("nope")
	require.Error(t, err)
}

func TestMetadata_Validate(t *testing.T) {
	t.Parallel()

	valid := wgcore.Metadata[plainExtensions]{
		Port:      12345,
		PublicKey: mustPublicKey(t),
	}
	require.NoError(t, valid.Validate())

	noPort := valid
	noPort.Port = 0
	require.ErrorIs(t, noPort.Validate(), validate.ErrInvalidConfig)

	for _, vector := range injectionVectors {
		badKey := valid
		badKey.PublicKey += vector
		require.ErrorIs(t, badKey.Validate(), validate.ErrInvalidConfig, "public key %q", vector)
	}

	badExtensions := wgcore.Metadata[failingExtensions]{
		Port:      valid.Port,
		PublicKey: valid.PublicKey,
	}
	require.ErrorIs(t, badExtensions.Validate(), errFailingExtensions)
}

func TestPeerRequest_Validate(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, wgcore.PeerRequest{PublicKey: "AAAA"}.Validate(), service.ErrInvalidPeer)

	private, err := wgcore.NewPrivateKey()
	require.NoError(t, err)

	public, err := wgcore.DerivePublicKey(private)
	require.NoError(t, err)
	require.NoError(t, wgcore.PeerRequest{PublicKey: public}.Validate())
}

func TestPeerResponse_JSONKeys(t *testing.T) {
	t.Parallel()

	plainMeta := wgcore.Metadata[plainExtensions]{
		Port:      12345,
		PublicKey: "k",
	}
	wantPlainMetaKeys := []string{"port", "public_key"}
	require.Equal(t, wantPlainMetaKeys, jsonKeys(t, plainMeta))

	obfMeta := wgcore.Metadata[extensions]{
		Extensions: extensions{Obfuscation: obfuscationFixture()},
		Port:       12345,
		PublicKey:  "k",
	}
	wantObfMetaKeys := []string{"extensions", "port", "public_key"}
	require.Equal(t, wantObfMetaKeys, jsonKeys(t, obfMeta))

	req := wgcore.PeerRequest{PublicKey: "k"}
	wantReqKeys := []string{"public_key"}
	require.Equal(t, wantReqKeys, jsonKeys(t, req))

	plainResp := wgcore.PeerResponse[plainExtensions]{
		Addresses: []string{"10.0.0.2/32"},
		MTU:       1420,
		Port:      12345,
		PublicKey: "k",
	}
	wantPlainRespKeys := []string{"addresses", "mtu", "port", "public_key"}
	require.Equal(t, wantPlainRespKeys, jsonKeys(t, plainResp))

	obfResp := wgcore.PeerResponse[extensions]{
		Addresses:  []string{"10.0.0.2/32"},
		Extensions: extensions{Obfuscation: obfuscationFixture()},
		MTU:        1420,
		Port:       12345,
		PublicKey:  "k",
	}
	wantObfRespKeys := []string{"addresses", "extensions", "mtu", "port", "public_key"}
	require.Equal(t, wantObfRespKeys, jsonKeys(t, obfResp))

	raw, err := json.Marshal(obfResp)
	require.NoError(t, err)

	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &decoded))

	var ext map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(decoded["extensions"], &ext))

	wantObfuscationKeys := []string{"h1", "h2", "h3", "h4", "jc", "jmax", "jmin", "s1", "s2", "s3", "s4"}
	require.Equal(t, wantObfuscationKeys, jsonKeys(t, ext["obfuscation"]))
}

func TestPeerResponse_Validate(t *testing.T) {
	t.Parallel()

	valid := peerPayloadFixture(t)

	distinct := make([]string, 9)
	for i := range distinct {
		distinct[i] = fmt.Sprintf("10.0.0.%d/32", i+2)
	}

	type response = wgcore.PeerResponse[plainExtensions]

	cases := []struct {
		mutate func(*response)
		name   string
		valid  bool
	}{
		{mutate: func(*response) {}, name: "valid", valid: true},
		{mutate: func(r *response) { r.Addresses = nil }, name: "no addresses"},
		{mutate: func(r *response) { r.Addresses = distinct }, name: "too many"},
		{mutate: func(r *response) { r.Addresses = []string{"10.0.0.0/24"} }, name: "wide prefix"},
		{mutate: func(r *response) { r.Addresses = []string{"127.0.0.1/32"} }, name: "loopback"},
		{mutate: func(r *response) { r.Addresses = []string{"224.0.0.1/32"} }, name: "multicast"},
		{mutate: func(r *response) { r.Addresses = []string{"::/128"} }, name: "unspecified"},
		{mutate: func(r *response) { r.Addresses = []string{"fe80::1/128"} }, name: "link local"},
		{mutate: func(r *response) { r.Addresses = []string{"10.0.0.2/32", "::ffff:10.0.0.2/128"} }, name: "duplicate"},
		{mutate: func(r *response) { r.MTU = 1279 }, name: "mtu below minimum"},
		{mutate: func(r *response) { r.Port = 0 }, name: "zero port"},
		{
			mutate: func(r *response) { r.PublicKey = base64.StdEncoding.EncodeToString(make([]byte, 31)) },
			name:   "short public key",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := valid
			tc.mutate(&r)

			err := r.Validate()
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, service.ErrInvalidPeer)
			}
		})
	}

	for _, vector := range injectionVectors {
		address := valid
		address.Addresses = []string{valid.Addresses[0] + vector}
		require.ErrorIs(t, address.Validate(), service.ErrInvalidPeer, "address %q", vector)

		key := valid
		key.PublicKey += vector
		require.ErrorIs(t, key.Validate(), service.ErrInvalidPeer, "public key %q", vector)
	}

	failing := wgcore.PeerResponse[failingExtensions]{
		Addresses: valid.Addresses,
		MTU:       valid.MTU,
		Port:      valid.Port,
		PublicKey: valid.PublicKey,
	}
	require.ErrorIs(t, failing.Validate(), service.ErrInvalidPeer)
}

func TestPreflightUserspace(t *testing.T) {
	t.Parallel()

	variant := wgcore.Variant{
		UserspaceBinary: "definitely-missing-binary",
	}

	cases := []struct {
		goos string
		name string
		want []service.PreflightCheck
	}{
		{
			goos: "windows",
			name: "windows",
		},
		{
			goos: "darwin",
			name: "darwin",
			want: []service.PreflightCheck{{
				Detail:   "definitely-missing-binary not found on PATH; wg-quick on macOS needs it",
				Name:     "service_userspace",
				OK:       true,
				Severity: service.SeverityWarning,
			}},
		},
		{
			goos: "linux",
			name: "linux",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, wgcore.PreflightUserspace(variant, tc.goos))
		})
	}
}
