package service_test

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

const (
	fakeBit     uint32       = 1 << 30
	fakeName    service.Name = "fake"
	goosWindows              = "windows"
)

type fakeConfig struct {
	Port uint16
}

type fakeServer struct {
	service.Server

	cfg fakeConfig
}

func binaryName(name string) string {
	if runtime.GOOS == goosWindows {
		return name + ".exe"
	}

	return name
}

func fakeClientDriver(bit uint32) service.ClientDriver[fakeConfig] {
	return service.ClientDriver[fakeConfig]{
		Bit:      bit,
		Defaults: func() fakeConfig { return fakeConfig{} },
		Metadata: func() service.Validator { return &fakePayload{} },
		New:      func(fakeConfig, service.Placement, zerolog.Logger) (service.Client, error) { return nil, nil },
	}
}

func requireUnpackCases(
	t *testing.T,
	prefix string,
	nullErr error,
	unpack func(json.RawMessage) (fakePayload, error),
	unpackPtr func(json.RawMessage) (*fakePayload, error),
) {
	t.Helper()

	cases := []struct {
		name       string
		payload    string
		want       fakePayload
		wantErr    error
		wantPrefix string
	}{
		{
			name:    "valid",
			payload: `{"name":"a"}`,
			want:    fakePayload{Name: "a"},
		},
		{
			name:       "malformed",
			payload:    `{`,
			wantPrefix: prefix,
		},
		{
			name:    "invalid",
			payload: `{"name":""}`,
			wantErr: errFakePayload,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := unpack(json.RawMessage(tc.payload))
			require.Equal(t, tc.want, got)

			switch {
			case tc.wantPrefix != "":
				require.Error(t, err)
				require.True(t, strings.HasPrefix(err.Error(), tc.wantPrefix))
			case tc.wantErr != nil:
				require.ErrorIs(t, err, tc.wantErr)
			default:
				require.NoError(t, err)
			}
		})
	}

	t.Run("null", func(t *testing.T) {
		t.Parallel()

		got, err := unpackPtr(json.RawMessage(`null`))
		require.ErrorIs(t, err, nullErr)
		require.Nil(t, got)
	})
}
