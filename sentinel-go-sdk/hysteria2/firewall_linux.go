//go:build linux

package hysteria2

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/rs/zerolog"
)

// A Linux node that runs a host firewall (firewalld or ufw) drops inbound UDP to the hysteria2
// listen port unless the port is opened, so clients cannot connect until an operator opens it by
// hand. The node opens its own port when it starts and removes the opening when it stops.
//
// The rules are runtime-only (firewalld without --permanent, ufw's live table, our own nft table):
// they are torn down on Stop and would in any case not survive a reboot, matching the daemon's own
// lifetime. nft is the fallback for a host with neither firewalld nor ufw but a default-drop input
// policy; it uses a dedicated table so removal cannot touch anyone else's rules.

const (
	fwNone      = ""
	fwFirewalld = "firewalld"
	fwUFW       = "ufw"
	fwNft       = "nft"
)

// runFunc runs a command and returns its combined output. It is a seam so tests can stub the host
// firewall tools.
type runFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

func defaultRun(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func nftTable(port uint16) string { return "sentinel-dvpn-hy2-" + strconv.Itoa(int(port)) }

// openInboundUDP opens port/udp through the active host firewall and returns the backend it used,
// or fwNone when no managed firewall is active and nft is unavailable. The caller passes the
// returned backend to closeInboundUDP on Stop.
func openInboundUDP(ctx context.Context, run runFunc, port uint16, log zerolog.Logger) (string, error) {
	p := strconv.Itoa(int(port))

	switch {
	case firewalldRunning(ctx, run):
		if out, err := run(ctx, "firewall-cmd", "--add-port="+p+"/udp"); err != nil {
			return fwNone, fmt.Errorf("firewalld: opening %s/udp: %w: %s", p, err, strings.TrimSpace(string(out)))
		}

		log.Info().Str("backend", fwFirewalld).Str("port", p).Msg("opened inbound udp")

		return fwFirewalld, nil
	case ufwActive(ctx, run):
		if out, err := run(ctx, "ufw", "allow", p+"/udp"); err != nil {
			return fwNone, fmt.Errorf("ufw: opening %s/udp: %w: %s", p, err, strings.TrimSpace(string(out)))
		}

		log.Info().Str("backend", fwUFW).Str("port", p).Msg("opened inbound udp")

		return fwUFW, nil
	case haveNft(ctx, run):
		table := nftTable(port)
		// Fresh dedicated table so open is idempotent and close removes only ours.
		_, _ = run(ctx, "nft", "delete", "table", "inet", table)

		stmts := [][]string{
			{"add", "table", "inet", table},
			{"add", "chain", "inet", table, "input", "{ type filter hook input priority -10 ; }"},
			{"add", "rule", "inet", table, "input", "udp", "dport", p, "accept"},
		}
		for _, a := range stmts {
			if out, err := run(ctx, "nft", a...); err != nil {
				_, _ = run(ctx, "nft", "delete", "table", "inet", table)

				return fwNone, fmt.Errorf("nft: opening %s/udp: %w: %s", p, err, strings.TrimSpace(string(out)))
			}
		}

		log.Info().Str("backend", fwNft).Str("port", p).Msg("opened inbound udp")

		return fwNft, nil
	default:
		// No managed firewall and no nft: the host is not filtering inbound, so nothing to open.
		return fwNone, nil
	}
}

// closeInboundUDP removes the opening made by openInboundUDP. It is best-effort: a failure is
// returned so Stop can surface it, but every backend's remove is harmless if the rule is already
// gone.
func closeInboundUDP(ctx context.Context, run runFunc, backend string, port uint16, log zerolog.Logger) error {
	p := strconv.Itoa(int(port))

	switch backend {
	case fwFirewalld:
		if out, err := run(ctx, "firewall-cmd", "--remove-port="+p+"/udp"); err != nil {
			return fmt.Errorf("firewalld: closing %s/udp: %w: %s", p, err, strings.TrimSpace(string(out)))
		}
	case fwUFW:
		if out, err := run(ctx, "ufw", "delete", "allow", p+"/udp"); err != nil {
			return fmt.Errorf("ufw: closing %s/udp: %w: %s", p, err, strings.TrimSpace(string(out)))
		}
	case fwNft:
		if out, err := run(ctx, "nft", "delete", "table", "inet", nftTable(port)); err != nil {
			return fmt.Errorf("nft: closing %s/udp: %w: %s", p, err, strings.TrimSpace(string(out)))
		}
	case fwNone:
		return nil
	default:
		return fmt.Errorf("closing %s/udp: unknown firewall backend %q", p, backend)
	}

	log.Info().Str("backend", backend).Str("port", p).Msg("closed inbound udp")

	return nil
}

func firewalldRunning(ctx context.Context, run runFunc) bool {
	out, err := run(ctx, "firewall-cmd", "--state")

	return err == nil && strings.Contains(string(out), "running")
}

func ufwActive(ctx context.Context, run runFunc) bool {
	out, err := run(ctx, "ufw", "status")

	return err == nil && strings.Contains(string(out), "Status: active")
}

func haveNft(ctx context.Context, run runFunc) bool {
	_, err := run(ctx, "nft", "--version")

	return err == nil && !errors.Is(err, exec.ErrNotFound)
}
