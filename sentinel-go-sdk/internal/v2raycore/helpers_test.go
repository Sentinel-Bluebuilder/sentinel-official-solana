package v2raycore_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/sentinel-official/sentinel-go-sdk/v3/internal/v2raycore"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/xray"
)

const (
	fakeName    = service.Name("v2test")
	fakeNameTwo = service.Name("xtest")

	protocolVLESS    = "vless"
	realityPublicKey = "-_v7-_v7-_v7-_v7-_v7-_v7-_v7-_v7-_v7-_v7-_s"
	tlsPin           = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

type fakeAPI struct {
	added           []string
	mu              sync.Mutex
	rejectRemoveTag string
	rejectTag       string
	removed         []string
	stats           map[string]int64
	statsErr        error
}

func (f *fakeAPI) AddUser(_ context.Context, _ grpc.ClientConnInterface, tag, protocol, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.rejectTag != "" && tag == f.rejectTag {
		return errors.New("rejected")
	}

	f.added = append(f.added, tag+" "+protocol+" "+id)

	return nil
}

func (f *fakeAPI) QueryStats(_ context.Context, _ grpc.ClientConnInterface, pattern string) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.statsErr != nil {
		return nil, f.statsErr
	}

	out := map[string]int64{}

	for name, value := range f.stats {
		if pattern == "" || strings.HasPrefix(name, pattern) {
			out[name] = value
		}
	}

	return out, nil
}

func (f *fakeAPI) RemoveUser(_ context.Context, _ grpc.ClientConnInterface, tag, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.rejectRemoveTag != "" && tag == f.rejectRemoveTag {
		return errors.New("rejected")
	}

	f.removed = append(f.removed, tag+" "+id)

	return nil
}

type fakeProcess struct {
	pid     int
	stopErr error
	stopped bool
	waitErr error
}

func (p *fakeProcess) Done() <-chan struct{} { return nil }

func (p *fakeProcess) Err() error { return nil }

func (p *fakeProcess) PID() int {
	if p.pid != 0 {
		return p.pid
	}

	return os.Getpid()
}

func (p *fakeProcess) Stop(context.Context) error {
	p.stopped = true

	return p.stopErr
}

func (p *fakeProcess) Wait(context.Context) error {
	return p.waitErr
}

type recordingStart struct {
	commands []service.Command
	process  service.Process
}

func (f *recordingStart) start(_ context.Context, c service.Command, _ zerolog.Logger) (service.Process, error) {
	f.commands = append(f.commands, c)

	return f.process, nil
}

func extensionsFixture() xray.Extensions {
	return xray.Extensions{
		Reality: &xray.Reality{
			Dest:        "example.com:443",
			Fingerprint: "chrome",
			PrivateKey:  "secret",
			PublicKey:   realityPublicKey,
			ServerName:  "example.com",
			ShortID:     "ab",
		},
	}
}

func inboundFixture() v2raycore.Inbound[xray.Extensions] {
	return v2raycore.Inbound[xray.Extensions]{
		Port:      443,
		Protocol:  protocolVLESS,
		Security:  v2raycore.SecurityTLS,
		TLSPin:    tlsPin,
		Transport: "ws",
	}
}

func jsonKeys(t *testing.T, v any) []string {
	t.Helper()

	raw, err := json.Marshal(v)
	require.NoError(t, err)

	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &m))

	return slices.Sorted(maps.Keys(m))
}

func requireConfDirUnset(t *testing.T, c service.Command) {
	t.Helper()

	want := []string{
		"V2RAY_LOCATION_CONFDIR",
		"XRAY_LOCATION_CONFDIR",
		"v2ray.location.confdir",
		"xray.location.confdir",
	}
	require.ElementsMatch(t, want, c.Unset)
}

func v2rayVariantFixture(a v2raycore.API) v2raycore.Variant {
	return v2raycore.Variant{
		API:                 a,
		Binary:              "v2ray",
		Name:                fakeName,
		SecurityPreferences: []string{v2raycore.SecurityTLS},
		Templates:           os.DirFS("../../v2ray"),
	}
}

func xrayVariantFixture(a v2raycore.API) v2raycore.Variant {
	return v2raycore.Variant{
		API:                 a,
		Binary:              "xray",
		Name:                fakeNameTwo,
		SecurityPreferences: []string{"reality", v2raycore.SecurityTLS},
		Templates:           os.DirFS("../../xray"),
	}
}
