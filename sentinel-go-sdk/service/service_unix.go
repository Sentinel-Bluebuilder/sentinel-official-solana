//go:build unix

package service

import (
	"fmt"
	"os/exec"
	"syscall"
)

func configure(*exec.Cmd) {}

func terminate(cmd *exec.Cmd) error {
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("sending terminate signal: %w", err)
	}

	return nil
}
