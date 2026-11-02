package v2raycore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/internal/v2raycore"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/v2ray"
	"github.com/sentinel-official/sentinel-go-sdk/v3/xray"
)

type failingExtensions struct {
	Channel chan int `json:"channel"`
}

func (e failingExtensions) Validate(v2raycore.Stream) error { return nil }

func TestServer_AddPeer_ResponseValidates(t *testing.T) {
	t.Parallel()

	t.Run("v2ray", func(t *testing.T) {
		t.Parallel()

		cfg := v2raycore.ServerConfig[v2ray.Extensions]{
			APIPort: 10085,
			Inbounds: []v2raycore.Inbound[v2ray.Extensions]{
				{Port: 443, Protocol: protocolVLESS, Security: v2raycore.SecurityTLS, Transport: "ws"},
				{Port: 8080, Protocol: "vmess", Security: "none", Transport: "tcp"},
			},
		}
		requireRoundTrip(t, v2rayVariantFixture(&fakeAPI{}), cfg)
	})

	t.Run("xray", func(t *testing.T) {
		t.Parallel()

		reality, err := xray.NewReality()
		require.NoError(t, err)

		cfg := xrayServerConfigFixture()
		cfg.Inbounds[2].Extensions.Reality = reality
		requireRoundTrip(t, xrayVariantFixture(&fakeAPI{}), cfg)
	})
}

func TestServer_AddPeer_Rollback(t *testing.T) {
	t.Parallel()

	cfg := xrayServerConfigFixture()
	a := &fakeAPI{rejectTag: cfg.Inbounds[len(cfg.Inbounds)-1].Tag()}
	recorder := &recordingStart{process: &fakeProcess{}}

	starter := service.WithProcessStarter(recorder.start)

	s := v2raycore.NewServerForTest(xrayVariantFixture(a), cfg, serverPlacementFixture(t), zerolog.Nop(), starter)
	require.NoError(t, s.Start(t.Context()))

	payload := v2raycore.PeerRequest{UUID: "00000000-0000-4000-8000-000000000002"}
	req, err := service.PackRequest(fakeNameTwo, payload)
	require.NoError(t, err)

	_, err = s.AddPeer(t.Context(), req)
	require.ErrorContains(t, err, "adding peer "+payload.UUID)
	require.Len(t, a.added, len(cfg.Inbounds)-1)

	wantRemoved := []string{"in-443 " + payload.UUID, "in-8080 " + payload.UUID}
	require.Equal(t, wantRemoved, a.removed)

	_, err = s.PeerStats(t.Context(), service.PeerID(payload.UUID))
	require.ErrorIs(t, err, service.ErrPeerNotFound)
}

func TestServer_Metadata_TLSPins(t *testing.T) {
	t.Parallel()

	recorder := &recordingStart{process: &fakeProcess{}}
	cfg := v2raycore.ServerConfig[v2ray.Extensions]{
		APIPort: 10085,
		Inbounds: []v2raycore.Inbound[v2ray.Extensions]{
			{Port: 443, Protocol: protocolVLESS, Security: v2raycore.SecurityTLS, Transport: "ws"},
			{Port: 8080, Protocol: "vmess", Security: "none", Transport: "tcp"},
		},
	}

	starter := service.WithProcessStarter(recorder.start)

	p := serverPlacementFixture(t)
	s := v2raycore.NewServerForTest(v2rayVariantFixture(&fakeAPI{}), cfg, p, zerolog.Nop(), starter)
	require.NoError(t, s.Start(t.Context()))

	t.Cleanup(func() {
		require.NoError(t, s.Stop(context.Background()))
	})

	var md v2raycore.Metadata[v2ray.Extensions]
	require.NoError(t, s.Metadata().Unpack(&md))
	require.Len(t, md.Inbounds, 2)
	require.Equal(t, v2raycore.ServerTLSPin(s), md.Inbounds[0].TLSPin)
	require.Empty(t, md.Inbounds[1].TLSPin)

	var rawMd map[string][]map[string]json.RawMessage
	require.NoError(t, s.Metadata().Unpack(&rawMd))

	wantNoneKeys := []string{"port", "protocol", "security", "transport"}
	require.Equal(t, wantNoneKeys, jsonKeys(t, rawMd["inbounds"][1]))
}

func TestServer_RemovePeer_RetriesOnlyRemainingInbounds(t *testing.T) {
	t.Parallel()

	cfg := xrayServerConfigFixture()
	last := cfg.Inbounds[len(cfg.Inbounds)-1].Tag()
	a := &fakeAPI{rejectRemoveTag: last}
	recorder := &recordingStart{process: &fakeProcess{}}

	s := v2raycore.NewServerForTest(
		xrayVariantFixture(a), cfg, serverPlacementFixture(t), zerolog.Nop(), service.WithProcessStarter(recorder.start),
	)
	require.NoError(t, s.Start(t.Context()))

	payload := v2raycore.PeerRequest{UUID: "00000000-0000-4000-8000-000000000002"}
	req, err := service.PackRequest(fakeNameTwo, payload)
	require.NoError(t, err)

	_, err = s.AddPeer(t.Context(), req)
	require.NoError(t, err)

	id := service.PeerID(payload.UUID)
	require.Error(t, s.RemovePeer(t.Context(), id))

	_, err = s.AddPeer(t.Context(), req)
	require.ErrorIs(t, err, service.ErrPeerExists)

	removed := len(a.removed)
	a.rejectRemoveTag = ""

	require.NoError(t, s.RemovePeer(t.Context(), id))
	require.Equal(t, []string{last + " " + payload.UUID}, a.removed[removed:])

	_, err = s.AddPeer(t.Context(), req)
	require.NoError(t, err)
}

func TestServer_Start_KillsStale(t *testing.T) {
	t.Parallel()

	p := serverPlacementFixture(t)
	recorder := &recordingStart{process: &fakeProcess{}}

	var gotFlag, gotConf string

	kill := service.WithStaleKiller(func(_ context.Context, _, _, flag, conf string) error {
		gotFlag, gotConf = flag, conf

		return errors.New("boom")
	})

	s := v2raycore.NewServerForTest(
		xrayVariantFixture(&fakeAPI{}), xrayServerConfigFixture(), p, zerolog.Nop(),
		service.WithProcessStarter(recorder.start), kill,
	)
	require.ErrorContains(t, s.Start(t.Context()), "stopping previous xray: boom")
	require.Equal(t, "-config", gotFlag)
	require.Equal(t, filepath.Join(p.ToolDir, "dvpn-n0.json"), gotConf)
	require.Empty(t, recorder.commands)
}

func TestServer_Start_ServesPeers(t *testing.T) {
	t.Parallel()

	a := &fakeAPI{stats: map[string]int64{}}
	proc := &fakeProcess{}
	recorder := &recordingStart{process: proc}
	p := serverPlacementFixture(t)
	conf := filepath.Join(p.ToolDir, "dvpn-n0.json")
	cfg := xrayServerConfigFixture()
	starter := service.WithProcessStarter(recorder.start)

	s := v2raycore.NewServerForTest(xrayVariantFixture(a), cfg, p, zerolog.Nop(), starter)
	require.Equal(t, fakeNameTwo, s.Name())
	require.NoError(t, s.Start(t.Context()))
	require.Len(t, recorder.commands, 1)
	require.Equal(t, "xray", recorder.commands[0].Path)

	require.Equal(t, []string{"run", "-config", conf}, recorder.commands[0].Args)
	requireConfDirUnset(t, recorder.commands[0])
	require.Len(t, v2raycore.ServerTLSPin(s), 64)

	raw, err := os.ReadFile(conf) //#nosec G304
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(raw, []byte("// "+service.ManagedMarker+"\n")))

	for _, name := range []string{"crt", "key"} {
		copied, err := os.ReadFile(p.ToolPath(".tls." + name)) //#nosec G304
		require.NoError(t, err)
		require.True(t, bytes.HasPrefix(copied, []byte("# "+service.ManagedMarker+"\n")))
		require.Contains(t, string(raw), p.ToolPath(".tls."+name))
	}

	var doc map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimPrefix(raw, []byte("// "+service.ManagedMarker+"\n")), &doc))

	inbounds := doc["inbounds"].([]any)
	require.Len(t, inbounds, len(cfg.Inbounds)+1)

	for i, in := range cfg.Inbounds {
		entry := inbounds[i+1].(map[string]any)
		require.Equal(t, in.Tag(), entry["tag"])
	}

	last := inbounds[3].(map[string]any)["streamSettings"].(map[string]any)
	realitySettings := last["realitySettings"].(map[string]any)
	require.Equal(t, "example.com:443", realitySettings["dest"])
	require.Equal(t, "secret", realitySettings["privateKey"])

	var md v2raycore.Metadata[xray.Extensions]
	require.NoError(t, s.Metadata().Unpack(&md))
	require.Len(t, md.Inbounds, 3)
	require.Equal(t, v2raycore.ServerTLSPin(s), md.Inbounds[0].TLSPin)
	require.Nil(t, md.Inbounds[0].Extensions.Reality)
	require.Empty(t, md.Inbounds[2].TLSPin)
	require.Equal(t, realityPublicKey, md.Inbounds[2].Extensions.Reality.PublicKey)
	require.NotContains(t, string(s.Metadata().Payload), "secret")

	var rawMd map[string][]map[string]json.RawMessage
	require.NoError(t, s.Metadata().Unpack(&rawMd))
	require.NotContains(t, rawMd["inbounds"][0], "extensions")
	require.Contains(t, rawMd["inbounds"][2], "extensions")

	payload := v2raycore.PeerRequest{UUID: "00000000-0000-4000-8000-000000000001"}
	req, err := service.PackRequest(fakeNameTwo, payload)
	require.NoError(t, err)

	resp, err := s.AddPeer(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, service.PeerID(payload.UUID), resp.ID)
	wantAdded := []string{"in-443 vless " + payload.UUID, "in-8080 vmess " + payload.UUID, "in-8443 vless " + payload.UUID}
	require.Equal(t, wantAdded, a.added)

	var peer v2raycore.PeerResponse[xray.Extensions]
	require.NoError(t, resp.Unpack(&peer))
	require.Equal(t, payload.UUID, peer.UUID)
	require.Len(t, peer.Inbounds, 3)

	_, err = s.AddPeer(t.Context(), req)
	require.ErrorIs(t, err, service.ErrPeerExists)

	a.stats["user>>>"+payload.UUID+">>>traffic>>>uplink"] = 10
	a.stats["user>>>"+payload.UUID+">>>traffic>>>downlink"] = 20

	stats, err := s.PeerStats(t.Context(), resp.ID)
	require.NoError(t, err)
	require.EqualValues(t, 10, stats.RxBytes)
	require.EqualValues(t, 20, stats.TxBytes)

	_, err = s.PeerStats(t.Context(), "unknown")
	require.ErrorIs(t, err, service.ErrPeerNotFound)
	require.ErrorContains(t, err, "unknown")

	require.NoError(t, s.RemovePeer(t.Context(), resp.ID))

	wantRemoved := []string{"in-443 " + payload.UUID, "in-8080 " + payload.UUID, "in-8443 " + payload.UUID}
	require.Equal(t, wantRemoved, a.removed)
	require.ErrorIs(t, s.RemovePeer(t.Context(), resp.ID), service.ErrPeerNotFound)

	require.NoError(t, s.Stop(t.Context()))
	require.True(t, proc.stopped)
	require.NoError(t, s.Wait(t.Context()))

	entries, err := os.ReadDir(p.ToolDir)
	require.NoError(t, err)
	require.Empty(t, entries)
	require.FileExists(t, filepath.Join(p.Dir, "tls.crt"))
	require.FileExists(t, filepath.Join(p.Dir, "tls.key"))
}

func TestServer_Start_UnencodableMetadata(t *testing.T) {
	t.Parallel()

	in := v2raycore.Inbound[failingExtensions]{
		Extensions: failingExtensions{Channel: make(chan int)},
		Port:       443,
		Protocol:   protocolVLESS,
		Security:   v2raycore.SecurityTLS,
		Transport:  "ws",
	}
	cfg := v2raycore.ServerConfig[failingExtensions]{
		APIPort:  10085,
		Inbounds: []v2raycore.Inbound[failingExtensions]{in},
	}
	recorder := &recordingStart{process: &fakeProcess{}}

	starter := service.WithProcessStarter(recorder.start)

	p := serverPlacementFixture(t)
	s := v2raycore.NewServerForTest(xrayVariantFixture(&fakeAPI{}), cfg, p, zerolog.Nop(), starter)

	err := s.Start(t.Context())
	require.ErrorContains(t, err, "encoding service metadata:")
	require.NotContains(t, err.Error(), "packing")

	entries, err := os.ReadDir(p.ToolDir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func requireRoundTrip[E v2raycore.Extensions](t *testing.T, v v2raycore.Variant, cfg v2raycore.ServerConfig[E]) {
	t.Helper()

	recorder := &recordingStart{process: &fakeProcess{}}
	starter := service.WithProcessStarter(recorder.start)
	s := v2raycore.NewServerForTest(v, cfg, serverPlacementFixture(t), zerolog.Nop(), starter)
	require.NoError(t, s.Start(t.Context()))

	req, err := service.PackRequest(v.Name, v2raycore.PeerRequest{UUID: "00000000-0000-4000-8000-000000000001"})
	require.NoError(t, err)

	resp, err := s.AddPeer(t.Context(), req)
	require.NoError(t, err)

	_, err = service.UnpackResponse[v2raycore.PeerResponse[E]](resp)
	require.NoError(t, err)

	_, err = service.UnpackMetadata[v2raycore.Metadata[E]](s.Metadata())
	require.NoError(t, err)
}

func serverPlacementFixture(t *testing.T) service.Placement {
	t.Helper()

	return service.Placement{
		Dir:       t.TempDir(),
		Interface: "dvpn-n0",
		ToolDir:   t.TempDir(),
	}
}

func xrayServerConfigFixture() v2raycore.ServerConfig[xray.Extensions] {
	return v2raycore.ServerConfig[xray.Extensions]{
		APIPort: 10085,
		Inbounds: []v2raycore.Inbound[xray.Extensions]{
			{Port: 443, Protocol: protocolVLESS, Security: v2raycore.SecurityTLS, Transport: "ws"},
			{Port: 8080, Protocol: "vmess", Security: v2raycore.SecurityTLS, Transport: "grpc"},
			{Extensions: extensionsFixture(), Port: 8443, Protocol: protocolVLESS, Security: "reality", Transport: "tcp"},
		},
	}
}
