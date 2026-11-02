package main

import (
	"context"
	"fmt"

	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

func deleteGroup(ctx context.Context, goos string, exec execFunc) error {
	lookup, _, remove := groupCommands(goos)
	if _, err := exec(ctx, lookup); err == nil {
		if _, err := exec(ctx, remove); err != nil {
			return fmt.Errorf("deleting group %s: %w", instance.GroupName, err)
		}
	}

	return nil
}

func ensureGroup(ctx context.Context, goos string, exec execFunc) error {
	lookup, create, _ := groupCommands(goos)
	if _, err := exec(ctx, lookup); err == nil {
		return nil
	}

	if _, err := exec(ctx, create); err != nil {
		return fmt.Errorf("creating group %s: %w", instance.GroupName, err)
	}

	return nil
}

func groupCommand(path string, args ...string) service.Command {
	return service.Command{
		Args: args,
		Path: path,
	}
}

func groupCommands(goos string) (lookup, create, remove service.Command) {
	switch goos {
	case "darwin":
		return groupCommand("dseditgroup", "-o", "read", instance.GroupName),
			groupCommand("dseditgroup", "-o", "create", instance.GroupName),
			groupCommand("dseditgroup", "-o", "delete", instance.GroupName)
	case "windows":
		return groupCommand("net", "localgroup", instance.GroupName),
			groupCommand("net", "localgroup", instance.GroupName, "/add"),
			groupCommand("net", "localgroup", instance.GroupName, "/delete")
	default:
		return groupCommand("getent", "group", instance.GroupName),
			groupCommand("groupadd", "--system", instance.GroupName),
			groupCommand("groupdel", instance.GroupName)
	}
}
