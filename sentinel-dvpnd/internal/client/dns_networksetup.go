package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
)

const (
	networksetupBackupFileName = "networksetup-dns.json.bak"
	networksetupBinary         = "networksetup"
	networksetupEmpty          = "Empty"
	networksetupHeaderPrefix   = "An asterisk"
	networksetupNoServers      = "There aren't any DNS Servers set"
)

type darwinDNSBackend struct {
	dnsBackend

	networksetup networksetupBackend
}

func newDarwinDNSBackend(kind, dir string, run runFunc) dnsBackend {
	backend := networksetupBackend{
		backupPath: filepath.Join(dir, networksetupBackupFileName),
		run:        run,
	}
	if kind == config.DNSNetworksetup {
		return backend
	}

	return darwinDNSBackend{
		dnsBackend:   noopBackend{},
		networksetup: backend,
	}
}

func (b darwinDNSBackend) Cleanup(ctx context.Context) error {
	return b.networksetup.Cleanup(ctx)
}

type networksetupBackend struct {
	backupPath string
	run        runFunc
}

func (b networksetupBackend) Apply(ctx context.Context, _ string, servers []string) error {
	saved, err := b.loadOrCreateBackup(ctx)
	if err != nil {
		return err
	}

	for _, svc := range slices.Sorted(maps.Keys(saved)) {
		args := append([]string{"-setdnsservers", svc}, servers...)
		if out, err := b.run(ctx, "", networksetupBinary, args...); err != nil {
			return fmt.Errorf("networksetup -setdnsservers %s: %w: %s", svc, err, out)
		}
	}

	return b.flush(ctx)
}

func (b networksetupBackend) Cleanup(ctx context.Context) error {
	if _, err := os.Stat(b.backupPath); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("checking dns backup: %w", err)
	}

	return b.Revert(ctx, "")
}

func (b networksetupBackend) Revert(ctx context.Context, _ string) error {
	raw, err := os.ReadFile(b.backupPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("reading dns backup: %w", err)
	}

	var saved map[string][]string
	if err := json.Unmarshal(raw, &saved); err != nil {
		return fmt.Errorf("decoding dns backup: %w", err)
	}

	for _, svc := range slices.Sorted(maps.Keys(saved)) {
		args := []string{"-setdnsservers", svc}
		if len(saved[svc]) == 0 {
			args = append(args, networksetupEmpty)
		} else {
			args = append(args, saved[svc]...)
		}

		if out, err := b.run(ctx, "", networksetupBinary, args...); err != nil {
			return fmt.Errorf("networksetup -setdnsservers %s: %w: %s", svc, err, out)
		}
	}

	if err := b.flush(ctx); err != nil {
		return err
	}

	if err := os.Remove(b.backupPath); err != nil {
		return fmt.Errorf("removing dns backup: %w", err)
	}

	return nil
}

func (b networksetupBackend) flush(ctx context.Context) error {
	if out, err := b.run(ctx, "", "dscacheutil", "-flushcache"); err != nil {
		return fmt.Errorf("dscacheutil -flushcache: %w: %s", err, out)
	}

	if out, err := b.run(ctx, "", "killall", "-HUP", "mDNSResponder"); err != nil {
		return fmt.Errorf("killall -HUP mDNSResponder: %w: %s", err, out)
	}

	return nil
}

func (b networksetupBackend) listNetworkServices(ctx context.Context) ([]string, error) {
	out, err := b.run(ctx, "", networksetupBinary, "-listallnetworkservices")
	if err != nil {
		return nil, fmt.Errorf("networksetup -listallnetworkservices: %w: %s", err, out)
	}

	return parseNetworkServices(out), nil
}

func (b networksetupBackend) loadOrCreateBackup(ctx context.Context) (map[string][]string, error) {
	raw, err := os.ReadFile(b.backupPath)
	switch {
	case err == nil:
		var saved map[string][]string
		if err := json.Unmarshal(raw, &saved); err != nil {
			return nil, fmt.Errorf("decoding dns backup: %w", err)
		}

		return saved, nil
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("reading dns backup: %w", err)
	}

	services, err := b.listNetworkServices(ctx)
	if err != nil {
		return nil, err
	}

	saved := make(map[string][]string, len(services))
	for _, svc := range services {
		out, err := b.run(ctx, "", networksetupBinary, "-getdnsservers", svc)
		if err != nil {
			return nil, fmt.Errorf("networksetup -getdnsservers %s: %w: %s", svc, err, out)
		}

		saved[svc] = parseDNSServers(out)
	}

	raw, err = json.Marshal(saved)
	if err != nil {
		return nil, fmt.Errorf("encoding dns backup: %w", err)
	}

	if err := os.WriteFile(b.backupPath, raw, 0o600); err != nil {
		return nil, fmt.Errorf("writing dns backup: %w", err)
	}

	return saved, nil
}

func parseDNSServers(out []byte) []string {
	if strings.Contains(string(out), networksetupNoServers) {
		return nil
	}

	var servers []string

	for line := range strings.SplitSeq(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			servers = append(servers, line)
		}
	}

	return servers
}

func parseNetworkServices(out []byte) []string {
	var services []string

	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, networksetupHeaderPrefix) || strings.HasPrefix(line, "*") {
			continue
		}

		services = append(services, line)
	}

	return services
}
