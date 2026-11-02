//go:build linux

package node_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/leftover"
	"github.com/sentinel-official/sentinel-dvpnd/internal/node"
	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/v2ray"
	"github.com/sentinel-official/sentinel-go-sdk/v3/wireguard"
)

func TestRuntime_ClearLeftovers_CleansOtherProtocols(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	remover := &testutil.RecordingInterfaceRemover{}
	node.SetInterfaceRemover(r, remover)

	cleanupErr := errors.New("leftover process still running")
	placements := map[service.Name]service.Placement{}

	node.SetServerCleanup(r, func(_ context.Context, name service.Name, p service.Placement) error {
		placements[name] = p

		return cleanupErr
	})

	err := node.ClearLeftovers(t.Context(), r)
	require.ErrorIs(t, err, cleanupErr)
	require.ErrorContains(t, err, "clearing v2ray leftovers")
	require.Equal(t, home.Loaded.Placement(v2ray.Name), placements[v2ray.Name])
	require.NotContains(t, placements, wireguard.Name)
	require.Equal(t, []string{home.Loaded.Tag.Interface()}, remover.Names)
}

func TestRuntime_ClearLeftovers_RemovesLeftoverCopies(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	node.SetInterfaceRemover(r, &testutil.RecordingInterfaceRemover{})
	node.SetServerCleanup(r, fakeServerCleanup)

	toolDir := func(name service.Name) string {
		dir := home.Loaded.Placement(name).ToolDir
		require.NoError(t, os.MkdirAll(dir, 0o700))

		return dir
	}
	base := home.Loaded.Tag.Interface()
	marked := "// " + service.ManagedMarker + "\n{}\n"
	leftoverCopy := filepath.Join(toolDir(v2ray.Name), base+".json")
	otherTag := filepath.Join(toolDir(v2ray.Name), base+"0.json")
	unmarked := filepath.Join(toolDir(wireguard.Name), base+".json")

	for path, content := range map[string]string{leftoverCopy: marked, otherTag: marked, unmarked: "{}\n"} {
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}

	require.NoError(t, node.ClearLeftovers(t.Context(), r))
	require.NoFileExists(t, leftoverCopy)
	require.FileExists(t, otherTag)
	require.FileExists(t, unmarked)
}

func TestRuntime_ClearLeftovers_ReportsLeftoverCopiesDir(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	node.SetInterfaceRemover(r, &testutil.RecordingInterfaceRemover{})
	node.SetServerCleanup(r, fakeServerCleanup)

	dir := home.Loaded.Placement(v2ray.Name).ToolDir
	require.NoError(t, os.MkdirAll(filepath.Dir(dir), 0o700))
	require.NoError(t, os.WriteFile(dir, nil, 0o600))

	err := node.ClearLeftovers(t.Context(), r)
	require.ErrorContains(t, err, "removing leftover copies in "+dir)
	require.NotContains(t, err.Error(), `"rm `)
}

func TestRuntime_ClearLeftovers_ReportsLeftoverInterface(t *testing.T) {
	t.Parallel()

	r, home, _ := newRuntime(t)
	remover := &testutil.RecordingInterfaceRemover{Err: leftover.ErrInterfaceExists}
	node.SetInterfaceRemover(r, remover)
	node.SetServerCleanup(r, fakeServerCleanup)

	require.ErrorIs(t, node.ClearLeftovers(t.Context(), r), leftover.ErrInterfaceExists)
	require.Equal(t, []string{home.Loaded.Tag.Interface()}, remover.Names)
}

func TestRuntime_ClearLeftovers_SkipsUnknownServer(t *testing.T) {
	t.Parallel()

	r, _, _ := newRuntime(t)
	node.SetInterfaceRemover(r, &testutil.RecordingInterfaceRemover{})
	node.SetServerCleanup(r, func(context.Context, service.Name, service.Placement) error {
		return service.ErrUnknownName
	})

	require.NoError(t, node.ClearLeftovers(t.Context(), r))
}

func fakeServerCleanup(context.Context, service.Name, service.Placement) error { return nil }
