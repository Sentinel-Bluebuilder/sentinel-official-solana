package client

import (
	"context"
	"path/filepath"
)

func KillSwitchLoaded(ctx context.Context, name string) (bool, error) {
	return pfLoaded(ctx, runPFCommand, name)
}

func newFirewall(dir, name string) firewallBackend {
	return pfFirewall{
		anchor:    name,
		run:       runPFCommand,
		tokenPath: filepath.Join(dir, pfTokenFileName),
	}
}
