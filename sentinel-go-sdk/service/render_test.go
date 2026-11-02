package service_test

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

func TestRenderBytes(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"templates/x.conf.tmpl": &fstest.MapFile{
			Data: []byte("port={{ .Port }}\naddrs={{ join .Addrs \",\" }}\npin={{ b64hex .Pin }}\n"),
		},
	}
	data := map[string]any{
		"Addrs": []string{"a", "b"},
		"Pin":   "deadbeef",
		"Port":  12345,
	}

	raw, err := service.RenderBytes(fsys, "templates/x.conf.tmpl", data)
	require.NoError(t, err)
	require.Equal(t, "port=12345\naddrs=a,b\npin=3q2+7w==\n", string(raw))
}

func TestWriteTemplate(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"templates/x.conf.tmpl": &fstest.MapFile{
			Data: []byte("port={{ .Port }}\naddrs={{ join .Addrs \",\" }}\npin={{ b64hex .Pin }}\n"),
		},
	}
	data := map[string]any{
		"Addrs": []string{"a", "b"},
		"Pin":   "deadbeef",
		"Port":  12345,
	}
	path := filepath.Join(t.TempDir(), "nested", "x.conf")

	require.NoError(t, service.WriteTemplate(fsys, "templates/x.conf.tmpl", path, data, "#"))

	raw, err := os.ReadFile(path) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, "# "+service.ManagedMarker+"\nport=12345\naddrs=a,b\npin=3q2+7w==\n", string(raw))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	foreign := filepath.Join(filepath.Dir(path), "foreign.conf")
	require.NoError(t, os.WriteFile(foreign, []byte("x\n"), 0o600))
	require.ErrorIs(t, service.WriteTemplate(fsys, "templates/x.conf.tmpl", foreign, data, "#"), service.ErrConfigNotOwned)
}
