package service_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

func TestCopyOwned(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "tls.key")
	require.NoError(t, os.WriteFile(src, []byte("key\n"), 0o600))

	dst := filepath.Join(dir, "tool", "dvpn-c0.tls.key")
	require.NoError(t, service.CopyOwned(src, dst, "#"))

	raw, err := os.ReadFile(dst) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, "# "+service.ManagedMarker+"\nkey\n", string(raw))

	foreign := filepath.Join(dir, "foreign.key")
	require.NoError(t, os.WriteFile(foreign, []byte("mine\n"), 0o600))
	require.ErrorIs(t, service.CopyOwned(src, foreign, "#"), service.ErrConfigNotOwned)
}

func TestOwnsFile(t *testing.T) {
	t.Parallel()

	cases := []struct {
		content string
		name    string
		symlink bool
		want    bool
	}{
		{content: "# " + service.ManagedMarker + "\nbody\n", name: "hash marker", want: true},
		{content: "// " + service.ManagedMarker + "\n{}\n", name: "json marker", want: true},
		{content: "[Interface]\n", name: "foreign"},
		{name: "missing"},
		{content: "# " + service.ManagedMarker + "\n", name: "symlink", symlink: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.symlink && runtime.GOOS == goosWindows {
				t.Skip("symlink creation requires privileges on Windows")
			}

			dir := t.TempDir()
			path := filepath.Join(dir, "dvpn-c0.conf")

			switch {
			case tc.symlink:
				target := filepath.Join(dir, "target.conf")
				require.NoError(t, os.WriteFile(target, []byte(tc.content), 0o600))
				require.NoError(t, os.Symlink(target, path))
			case tc.content != "":
				require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o600))
			}

			owned, err := service.OwnsFile(path)
			require.NoError(t, err)
			require.Equal(t, tc.want, owned)
		})
	}
}

func TestRemoveOwned(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	hash := "# " + service.ManagedMarker + "\n"
	files := map[string]string{
		"dvpn-n1.auth.txt": "user\npass\n",
		"dvpn-n1.conf":     hash,
		"dvpn-n1.json":     "// " + service.ManagedMarker + "\n{}\n",
		"dvpn-n1.notes":    "mine\n",
		"dvpn-n1.tls.key":  hash + "key\n",
		"dvpn-n10.conf":    hash,
	}

	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
	}

	kept := []string{"dvpn-n1.notes", "dvpn-n10.conf"}

	if runtime.GOOS != goosWindows {
		target := filepath.Join(t.TempDir(), "owned.conf")
		require.NoError(t, os.WriteFile(target, []byte(hash), 0o600))
		require.NoError(t, os.Symlink(target, filepath.Join(dir, "dvpn-n1.link")))

		kept = append(kept, "dvpn-n1.link")
	}

	require.NoError(t, service.RemoveOwned(dir, "dvpn-n1"))

	for _, name := range []string{"dvpn-n1.auth.txt", "dvpn-n1.conf", "dvpn-n1.json", "dvpn-n1.tls.key"} {
		require.NoFileExists(t, filepath.Join(dir, name))
	}

	for _, name := range kept {
		require.FileExists(t, filepath.Join(dir, name))
	}

	require.NoError(t, service.RemoveOwned(filepath.Join(dir, "missing"), "dvpn-n1"))
}

func TestRemoveOwned_KeepsAuthBesideForeignConf(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for name, content := range map[string]string{"dvpn-c0.auth.txt": "user\npass\n", "dvpn-c0.conf": "client\n"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
	}

	require.NoError(t, service.RemoveOwned(dir, "dvpn-c0"))
	require.FileExists(t, filepath.Join(dir, "dvpn-c0.auth.txt"))
	require.FileExists(t, filepath.Join(dir, "dvpn-c0.conf"))
}

func TestWriteOwned(t *testing.T) {
	t.Parallel()

	marked := "# " + service.ManagedMarker + "\n"
	cases := []struct {
		comment  string
		existing string
		name     string
		want     string
		wantErr  error
	}{
		{comment: "#", name: "new file", want: marked + "body\n"},
		{comment: "//", name: "json marker", want: "// " + service.ManagedMarker + "\nbody\n"},
		{comment: "", name: "no marker", want: "body\n"},
		{comment: "#", existing: marked + "old body\n", name: "owned file", want: marked + "body\n"},
		{
			comment:  "#",
			existing: "[Interface]\n",
			name:     "foreign file",
			want:     "[Interface]\n",
			wantErr:  service.ErrConfigNotOwned,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "sentinel-dvpn", "dvpn-c0.conf")
			if tc.existing != "" {
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
				require.NoError(t, os.WriteFile(path, []byte(tc.existing), 0o600))
			}

			err := service.WriteOwned(path, []byte("body\n"), tc.comment)
			require.ErrorIs(t, err, tc.wantErr)

			raw, err := os.ReadFile(path) //#nosec G304
			require.NoError(t, err)
			require.Equal(t, tc.want, string(raw))

			info, err := os.Stat(path)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		})
	}
}

func TestWriteOwned_RejectsSymlink(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == goosWindows {
		t.Skip("symlink creation requires privileges on Windows")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target.conf")
	owned := "# " + service.ManagedMarker + "\nold\n"
	require.NoError(t, os.WriteFile(target, []byte(owned), 0o600))

	path := filepath.Join(dir, "dvpn-c0.conf")
	require.NoError(t, os.Symlink(target, path))

	err := service.WriteOwned(path, []byte("body\n"), "#")
	require.ErrorIs(t, err, service.ErrConfigNotOwned)

	raw, err := os.ReadFile(target) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, owned, string(raw))
}
