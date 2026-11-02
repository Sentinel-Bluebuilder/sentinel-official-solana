//go:build unix

package preflight

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

var errNotRoot = errors.New("insufficient privileges: the client role must run as root")

func checkKillSwitchBackend(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", errKillSwitchBackendUnusable, err)
	}

	tool := "nft"
	if runtime.GOOS == "darwin" {
		tool = "pfctl"
	}

	if _, err := service.LookPath(tool); err != nil {
		return fmt.Errorf("%w: %w", errKillSwitchBackendUnusable, err)
	}

	return nil
}

func checkPrivileges() error {
	if runtime.GOOS == "darwin" && os.Geteuid() != 0 {
		return errNotRoot
	}

	return nil
}
