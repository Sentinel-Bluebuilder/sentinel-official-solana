package preflight

import (
	"context"
)

func CheckDNSBackend(backend string) error { return checkDNSBackend(backend) }

func CheckKillSwitchBackend(ctx context.Context) error { return checkKillSwitchBackend(ctx) }

func CheckPrivileges() error { return checkPrivileges() }

func DNSBackendHelp(goos string) string { return dnsBackendHelp(goos) }
