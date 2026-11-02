package v2raycore_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/internal/v2raycore"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/v2ray"
	"github.com/sentinel-official/sentinel-go-sdk/v3/xray"
)

//nolint:paralleltest // forks a shell and binds the shared API port
func TestClient_Cleanup_KillsOrphan(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell process fixture requires Unix")
	}

	p := clientPlacementFixture(t)
	conf := p.ToolPath(".json")
	cmd := exec.CommandContext(
		t.Context(), "sh", "-c",
		"trap 'kill \"$child\"' TERM; sleep 30 & child=$!; echo ready; wait \"$child\"",
		"fixture", "-config", conf,
	)
	ready, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	_, err = bufio.NewReader(ready).ReadString('\n')
	require.NoError(t, err)

	done := make(chan struct{})

	go func() {
		_ = cmd.Wait()

		close(done)
	}()

	t.Cleanup(func() {
		_ = cmd.Process.Kill()

		<-done
	})

	cfg := v2raycore.ClientConfig[v2ray.Extensions]{
		APIPort:   2323,
		ProxyPort: 1080,
	}
	recorder := &recordingStart{process: &fakeProcess{pid: cmd.Process.Pid}}
	variant := v2rayVariantFixture(&fakeAPI{})
	variant.Binary = "sh"
	c := v2raycore.NewClientForTest(variant, cfg, p, zerolog.Nop(), service.WithProcessStarter(recorder.start))
	resp := packedResponseFixture(t, c, fakeName)
	require.NoError(t, c.Start(t.Context(), "203.0.113.1", resp))
	require.FileExists(t, filepath.Join(p.Dir, "sh.pid"))
	require.FileExists(t, conf)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	require.NoError(t, c.Cleanup(ctx))

	select {
	case <-done:
	case <-ctx.Done():
		require.Fail(t, "orphan process did not exit")
	}

	require.NoFileExists(t, filepath.Join(p.Dir, "sh.pid"))
	require.NoFileExists(t, conf)
}

func TestClient_Cleanup_RemovesToolCopies(t *testing.T) {
	t.Parallel()

	p := clientPlacementFixture(t)
	cfg := v2raycore.ClientConfig[xray.Extensions]{
		APIPort:   2323,
		ProxyPort: 1080,
	}
	c := v2raycore.NewClientForTest(xrayVariantFixture(&fakeAPI{}), cfg, p, zerolog.Nop())
	resp := v2raycore.PeerResponse[xray.Extensions]{
		Inbounds: []v2raycore.Inbound[xray.Extensions]{inboundFixture()},
		UUID:     "00000000-0000-4000-8000-000000000001",
	}

	conf, err := v2raycore.ClientWriteConf(c, "203.0.113.1", resp)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(p.ToolDir, "dvpn-c0.json"), conf)

	raw, err := os.ReadFile(conf) //#nosec G304
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(raw), "// "+service.ManagedMarker+"\n"))

	require.NoError(t, c.Cleanup(t.Context()))
	require.NoFileExists(t, conf)
}

func TestClient_Start_RejectsInUseAPIPort(t *testing.T) {
	t.Parallel()

	cases := []struct {
		make func(v2raycore.API) v2raycore.Variant
		name string
	}{
		{
			make: v2rayVariantFixture,
			name: "v2ray",
		},
		{
			make: xrayVariantFixture,
			name: "xray",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, listener.Close()) })

			port := uint16(listener.Addr().(*net.TCPAddr).Port)
			cfg := v2raycore.ClientConfig[v2ray.Extensions]{
				APIPort:   port,
				ProxyPort: 1080,
			}
			recorder := &recordingStart{process: &fakeProcess{}}
			p := clientPlacementFixture(t)
			v := tc.make(&fakeAPI{})
			c := v2raycore.NewClientForTest(v, cfg, p, zerolog.Nop(), service.WithProcessStarter(recorder.start))
			resp := packedResponseFixture(t, c, v.Name)

			err = c.Start(t.Context(), "203.0.113.1", resp)
			require.ErrorContains(t, err, fmt.Sprintf("127.0.0.1:%d", port))
			require.Empty(t, recorder.commands)
			require.NoFileExists(t, p.ToolPath(".json"))
		})
	}
}

func TestClient_Start_RejectsInvalidPeer(t *testing.T) {
	t.Parallel()

	foreign := "00000000-0000-4000-8000-000000000009"
	cases := []struct {
		mutate func(*service.PeerResponse)
		name   string
	}{
		{
			mutate: func(r *service.PeerResponse) { r.Name = fakeNameTwo },
			name:   "wrong service",
		},
		{
			mutate: func(r *service.PeerResponse) { r.ID = service.PeerID(foreign) },
			name:   "foreign id",
		},
		{
			mutate: func(r *service.PeerResponse) {
				r.Payload = bytes.ReplaceAll(r.Payload, []byte(r.ID), []byte(foreign))
			},
			name: "foreign payload uuid",
		},
		{
			mutate: func(r *service.PeerResponse) { r.Payload = []byte(`{"inbounds":[],"uuid":"` + r.ID + `"}`) },
			name:   "no inbounds",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := clientPlacementFixture(t)
			cfg := v2raycore.ClientConfig[v2ray.Extensions]{
				APIPort:   2323,
				ProxyPort: 1080,
			}
			recorder := &recordingStart{process: &fakeProcess{}}
			starter := service.WithProcessStarter(recorder.start)
			c := v2raycore.NewClientForTest(v2rayVariantFixture(&fakeAPI{}), cfg, p, zerolog.Nop(), starter)
			resp := packedResponseFixture(t, c, fakeName)
			tc.mutate(&resp)

			require.ErrorIs(t, c.Start(t.Context(), "203.0.113.1", resp), service.ErrInvalidPeer)
			require.NoFileExists(t, p.ToolPath(".json"))
			require.Empty(t, recorder.commands)
		})
	}
}

//nolint:paralleltest // binds the shared API port
func TestClient_Start_RunsAndStops(t *testing.T) {
	a := &fakeAPI{stats: map[string]int64{
		"outbound>>>out>>>traffic>>>downlink": 7,
		"outbound>>>out>>>traffic>>>uplink":   9,
	}}
	proc := &fakeProcess{}
	recorder := &recordingStart{process: proc}
	p := clientPlacementFixture(t)
	cfg := v2raycore.ClientConfig[v2ray.Extensions]{
		APIPort:   2323,
		ProxyPort: 1080,
	}

	starter := service.WithProcessStarter(recorder.start)
	c := v2raycore.NewClientForTest(v2rayVariantFixture(a), cfg, p, zerolog.Nop(), starter)
	require.Empty(t, c.Interface())
	require.Equal(t, "127.0.0.1:1080", c.ProxyEndpoint())
	require.Equal(t, fakeName, c.Name())

	req, err := c.PeerRequest(t.Context())
	require.NoError(t, err)
	require.Equal(t, fakeName, req.Name)

	var pr v2raycore.PeerRequest
	require.NoError(t, req.Unpack(&pr))
	require.NoError(t, pr.Validate())

	peer := v2raycore.PeerResponse[v2ray.Extensions]{
		Inbounds: []v2raycore.Inbound[v2ray.Extensions]{
			{
				Port:      443,
				Protocol:  protocolVLESS,
				Security:  v2raycore.SecurityTLS,
				TLSPin:    tlsPin,
				Transport: "ws",
			},
		},
		UUID: pr.UUID,
	}

	resp, err := service.PackResponse(fakeName, service.PeerID(pr.UUID), peer)
	require.NoError(t, err)
	require.NoError(t, c.Start(t.Context(), "203.0.113.1", resp))
	require.Len(t, recorder.commands, 1)

	wantArgs := []string{"run", "-config", p.ToolPath(".json")}
	require.Equal(t, wantArgs, recorder.commands[0].Args)
	requireConfDirUnset(t, recorder.commands[0])

	stats, err := c.Stats(t.Context())
	require.NoError(t, err)
	require.EqualValues(t, 7, stats.RxBytes)
	require.EqualValues(t, 9, stats.TxBytes)

	pidPath := filepath.Join(p.Dir, "v2ray.pid")
	require.FileExists(t, pidPath)
	require.NoError(t, c.Stop(t.Context()))
	require.NoError(t, c.Wait(t.Context()))
	require.NoFileExists(t, pidPath)
	require.NoFileExists(t, p.ToolPath(".json"))
	require.True(t, proc.stopped)
}

//nolint:paralleltest // binds the shared API port
func TestClient_Start_RunsCheckVersion(t *testing.T) {
	cases := []struct {
		err  error
		name string
	}{
		{
			err:  errors.New("V2Ray 4.34.0 ignores run -config; install V2Ray 5"),
			name: "refused",
		},
		{
			name: "accepted",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := v2raycore.ClientConfig[v2ray.Extensions]{
				APIPort:   2323,
				ProxyPort: 1080,
			}
			recorder := &recordingStart{process: &fakeProcess{}}
			p := clientPlacementFixture(t)
			v := v2rayVariantFixture(&fakeAPI{})
			v.CheckVersion = func(context.Context) error { return tc.err }
			c := v2raycore.NewClientForTest(v, cfg, p, zerolog.Nop(), service.WithProcessStarter(recorder.start))
			resp := packedResponseFixture(t, c, v.Name)

			err := c.Start(t.Context(), "203.0.113.1", resp)
			if tc.err != nil {
				require.EqualError(t, err, "starting v2ray: "+tc.err.Error())
				require.Empty(t, recorder.commands)
				require.NoFileExists(t, p.ToolPath(".json"))

				return
			}

			require.NoError(t, err)
			require.Len(t, recorder.commands, 1)
			require.NoError(t, c.Stop(t.Context()))
		})
	}
}

func TestClient_WriteConf(t *testing.T) {
	t.Parallel()

	tlsInbound := inboundFixture()
	realityInbound := v2raycore.Inbound[xray.Extensions]{
		Extensions: extensionsFixture(),
		Port:       8443,
		Protocol:   protocolVLESS,
		Security:   "reality",
		Transport:  "tcp",
	}
	trojanInbound := tlsInbound
	trojanInbound.Protocol = "trojan"
	visionInbound := realityInbound
	visionInbound.Flow = "xtls-rprx-vision"

	t.Run("v2ray", func(t *testing.T) {
		t.Parallel()

		in := v2raycore.Inbound[v2ray.Extensions]{
			Port:      443,
			Protocol:  protocolVLESS,
			Security:  v2raycore.SecurityTLS,
			TLSPin:    tlsPin,
			Transport: "ws",
		}
		requireGoldenConf(t, v2rayVariantFixture, []v2raycore.Inbound[v2ray.Extensions]{in}, "client_v2ray.json")
	})

	t.Run("xray tls", func(t *testing.T) {
		t.Parallel()

		inbounds := []v2raycore.Inbound[xray.Extensions]{trojanInbound}
		requireGoldenConf(t, xrayVariantFixture, inbounds, "client_xray_tls.json")
	})

	t.Run("xray vision", func(t *testing.T) {
		t.Parallel()

		inbounds := []v2raycore.Inbound[xray.Extensions]{visionInbound}
		requireGoldenConf(t, xrayVariantFixture, inbounds, "client_xray_vision.json")
	})
}

func clientPlacementFixture(t *testing.T) service.Placement {
	t.Helper()

	return service.Placement{
		Dir:       t.TempDir(),
		Interface: "dvpn-c0",
		ToolDir:   t.TempDir(),
	}
}

func packedResponseFixture[E v2raycore.Extensions](
	t *testing.T,
	c *v2raycore.Client[E],
	name service.Name,
) service.PeerResponse {
	t.Helper()

	req, err := c.PeerRequest(t.Context())
	require.NoError(t, err)

	var pr v2raycore.PeerRequest
	require.NoError(t, req.Unpack(&pr))

	p := v2raycore.PeerResponse[v2ray.Extensions]{
		Inbounds: []v2raycore.Inbound[v2ray.Extensions]{
			{
				Port:      443,
				Protocol:  protocolVLESS,
				Security:  v2raycore.SecurityTLS,
				TLSPin:    tlsPin,
				Transport: "ws",
			},
		},
		UUID: pr.UUID,
	}
	resp, err := service.PackResponse(name, service.PeerID(pr.UUID), p)
	require.NoError(t, err)

	return resp
}

func requireGoldenConf[E v2raycore.Extensions](
	t *testing.T,
	variant func(v2raycore.API) v2raycore.Variant,
	inbounds []v2raycore.Inbound[E],
	golden string,
) {
	t.Helper()

	cfg := v2raycore.ClientConfig[E]{
		APIPort:   2323,
		ProxyPort: 1080,
	}
	p := clientPlacementFixture(t)
	c := v2raycore.NewClientForTest(variant(&fakeAPI{}), cfg, p, zerolog.Nop())
	resp := v2raycore.PeerResponse[E]{
		Inbounds: inbounds,
		UUID:     "00000000-0000-4000-8000-000000000001",
	}

	path, err := v2raycore.ClientWriteConf(c, "203.0.113.1", resp)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(p.ToolDir, "dvpn-c0.json"), path)

	raw, err := os.ReadFile(path) //#nosec G304
	require.NoError(t, err)

	want, err := os.ReadFile(filepath.Join("testdata", golden)) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, string(want), string(raw))

	var doc map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimPrefix(raw, []byte("// "+service.ManagedMarker+"\n")), &doc))
}
