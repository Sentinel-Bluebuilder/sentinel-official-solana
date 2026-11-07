//go:build !windows

package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeTool(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700))
	require.NoError(t, os.Chmod(path, mode))

	return path
}

func openDir(t *testing.T, mode os.FileMode) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, mode))

	return dir
}

func TestLookPath_SkipsUserWritableDir(t *testing.T) {
	planted := openDir(t, 0o777)
	writeTool(t, planted, "trust-probe", 0o755)

	safe := t.TempDir()
	want := writeTool(t, safe, "trust-probe", 0o755)

	t.Setenv("PATH", planted+string(os.PathListSeparator)+safe)

	got, err := LookPath("trust-probe")
	require.NoError(t, err)
	require.Equal(t, want, got)

	t.Setenv("PATH", planted)

	_, err = LookPath("trust-probe")
	require.ErrorIs(t, err, ErrUntrustedBinary)
	require.ErrorContains(t, err, planted)
}

func TestLookPath_RejectsWritableBinary(t *testing.T) {
	path := writeTool(t, t.TempDir(), "trust-probe", 0o757)

	_, err := LookPath(path)
	require.ErrorIs(t, err, ErrUntrustedBinary)
}

func TestLookPath_RejectsOtherOwner(t *testing.T) {
	path := writeTool(t, openDir(t, 0o755), "trust-probe", 0o755)

	_, err := LookPath(path)
	require.NoError(t, err)

	// Stand in for the root daemon: the tool now belongs to an ordinary user, as a planted one does.
	real := trustEUID
	trustEUID = func() int { return 0 }

	defer func() { trustEUID = real }()

	_, err = LookPath(path)
	require.ErrorIs(t, err, ErrUntrustedBinary)
}

func TestLookPath_AllowsStickyFolderAbove(t *testing.T) {
	sticky := openDir(t, 0o777|os.ModeSticky)
	dir := filepath.Join(sticky, "tools")
	require.NoError(t, os.Mkdir(dir, 0o755))
	path := writeTool(t, dir, "trust-probe", 0o755)

	got, err := LookPath(path)
	require.NoError(t, err)
	require.Equal(t, path, got)
}

func TestLookPath_RejectsSymlinkIntoUntrustedDir(t *testing.T) {
	planted := openDir(t, 0o777)
	target := writeTool(t, planted, "trust-probe", 0o755)

	safe := t.TempDir()
	link := filepath.Join(safe, "trust-probe")
	require.NoError(t, os.Symlink(target, link))

	t.Setenv("PATH", safe)

	_, err := LookPath("trust-probe")
	require.ErrorIs(t, err, ErrUntrustedBinary)
}

func TestLookPath_RejectsRelativePath(t *testing.T) {
	_, err := LookPath(filepath.Join(".", "bin", "tool"))
	require.Error(t, err)
}

func TestRun_ChildPathHoldsOnlyTrustedDirs(t *testing.T) {
	planted := openDir(t, 0o777)
	t.Setenv("PATH", planted+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")

	out, err := Run(context.Background(), Command{Path: "sh", Args: []string{"-c", `printf %s "$PATH"`}})
	require.NoError(t, err)

	dirs := strings.Split(string(out), string(os.PathListSeparator))
	require.NotContains(t, dirs, planted)
	require.Contains(t, dirs, "/usr/bin")
}

func TestCommandContext_UnresolvedToolDoesNotStart(t *testing.T) {
	planted := openDir(t, 0o777)
	writeTool(t, planted, "trust-probe", 0o755)
	t.Setenv("PATH", planted)

	cmd := CommandContext(context.Background(), "trust-probe")
	require.ErrorIs(t, cmd.Run(), ErrUntrustedBinary)
}
