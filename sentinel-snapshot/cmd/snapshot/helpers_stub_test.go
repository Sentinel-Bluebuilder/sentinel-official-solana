//go:build integration && !unsafeskipverify

package main

import (
	"os"
	"testing"
)

type fileStat struct {
	sizeBytes       int64
	modTimeUnixNano int64
}

func archiveFileStats(t *testing.T, dir string) map[string]fileStat {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}

	out := make(map[string]fileStat, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatalf("Info(%s/%s): %v", dir, e.Name(), err)
		}

		out[e.Name()] = fileStat{sizeBytes: info.Size(), modTimeUnixNano: info.ModTime().UnixNano()}
	}

	return out
}
