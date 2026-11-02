//go:build windows

package wireguard

import (
	"os"
	"path/filepath"
)

//nolint:gochecknoinits
func init() {
	lookupDirs = windowsLookupDirs
}

func windowsLookupDirs() []string {
	programFiles := os.Getenv("ProgramFiles")
	if programFiles == "" {
		return nil
	}

	return []string{filepath.Join(programFiles, "WireGuard")}
}
