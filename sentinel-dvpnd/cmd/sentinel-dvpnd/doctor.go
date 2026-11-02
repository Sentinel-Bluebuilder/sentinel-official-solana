package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net"
	"net/netip"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/spf13/cobra"

	"github.com/sentinel-official/sentinel-dvpnd/internal/client"
	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-dvpnd/internal/daemon"
	"github.com/sentinel-official/sentinel-dvpnd/internal/preflight"
	"github.com/sentinel-official/sentinel-dvpnd/internal/rpcpin"
	"github.com/sentinel-official/sentinel-dvpnd/internal/store"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/wireguard"
)

const (
	backgroundCheckName  = "background"
	killSwitchCheckName  = "kill_switch"
	metricsCheckName     = "metrics"
	permissionsCheckName = "permissions"
	roleCheckName        = "role"

	formatJSON  = "json"
	formatTable = "table"

	instanceDirMode = 0o750
	privateDirMode  = 0o700
	privateFileMode = 0o600
	socketMode      = 0o660

	maxPermissionPaths = 5
)

var errInvalidOutput = errors.New("output must be json or table")

type killSwitchProbe struct {
	daemonRunning func(dir string) (bool, error)
	kernelLoaded  func(ctx context.Context, name string) (bool, error)
}

func newKillSwitchProbe() killSwitchProbe {
	return killSwitchProbe{
		daemonRunning: daemon.Locked,
		kernelLoaded:  client.KillSwitchLoaded,
	}
}

func checkInstancePermissions(dir string, gid int) service.PreflightCheck {
	var exposed, narrow []string

	root, err := filepath.EvalSymlinks(dir)
	if err == nil {
		err = filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			info, err := os.Lstat(path)
			if err != nil {
				return fmt.Errorf("inspecting %s: %w", path, err)
			}

			want, ok := expectedMode(info, path == root)
			if !ok {
				return nil
			}

			name, _ := filepath.Rel(root, path)
			got := info.Mode().Perm()
			finding := fmt.Sprintf("%s is %04o, want %04o", name, got, want)

			switch {
			case got&^want != 0:
				exposed = append(exposed, finding)
			case got != want:
				narrow = append(narrow, finding)
			}

			if actual, ok := fileGroupID(info); ok && gid >= 0 && path == root && actual != gid {
				narrow = append(narrow, fmt.Sprintf("%s has group %d, want group %s", name, actual, instance.GroupName))
			}

			return nil
		})
	}

	if err != nil {
		narrow = append(narrow, "cannot inspect "+dir+": "+err.Error())
	}

	findings := slices.Concat(exposed, narrow)
	detail := strings.Join(findings[:min(len(findings), maxPermissionPaths)], "; ")

	switch {
	case len(exposed) > 0:
		return service.NewPreflightCheck(permissionsCheckName, service.SeverityError, detail)
	case len(narrow) > 0:
		return service.NewPreflightCheck(permissionsCheckName, service.SeverityWarning, detail)
	default:
		return service.NewPreflightCheck(permissionsCheckName, service.SeverityOK, "instance folder modes are as expected")
	}
}

func checkLeftoverKillSwitch(dir string, probe killSwitchProbe) []service.PreflightCheck {
	running, err := probe.daemonRunning(dir)
	switch {
	case err != nil:
		return newKillSwitchChecks(
			service.SeverityWarning,
			"a non-persistent kill switch row is recorded; cannot tell whether the daemon runs ("+
				err.Error()+"); checks run unpinned",
		)
	case running:
		return newKillSwitchChecks(
			service.SeverityOK, "a non-persistent kill switch row belongs to the running daemon's session; checks run unpinned",
		)
	default:
		return newKillSwitchChecks(
			service.SeverityWarning, "a non-persistent kill switch row is left over; the next run releases it",
		)
	}
}

func checkMetricsListen(
	own config.Metrics, others map[instance.Tag]config.Metrics, unreadable []string,
) service.PreflightCheck {
	if !own.Enabled {
		return service.NewPreflightCheck(metricsCheckName, service.SeverityOK, "metrics are disabled")
	}

	for _, tag := range slices.Sorted(maps.Keys(others)) {
		if other := others[tag]; other.Enabled && metricsOverlap(own.Listen, other.Listen) {
			return service.NewPreflightCheck(metricsCheckName, service.SeverityError, fmt.Sprintf(
				"instance %s also serves metrics on %s; give each instance its own metrics.listen", tag, other.Listen,
			))
		}
	}

	if len(unreadable) > 0 {
		return service.NewPreflightCheck(
			metricsCheckName, service.SeverityWarning, "cannot read installed instances: "+strings.Join(unreadable, "; "),
		)
	}

	return service.NewPreflightCheck(
		metricsCheckName, service.SeverityOK, "no installed instance shares the metrics address",
	)
}

func checkPersistedKillSwitch(ctx context.Context, dir, name string, probe killSwitchProbe) []service.PreflightCheck {
	const suffix = "; checks use its stored addresses; StopSession releases it"

	running, err := probe.daemonRunning(dir)
	if err != nil {
		return newKillSwitchChecks(
			service.SeverityWarning,
			"persisted kill switch is recorded; cannot tell whether the daemon runs ("+err.Error()+")"+suffix,
		)
	}

	loaded, err := probe.kernelLoaded(ctx, name)
	switch {
	case err != nil:
		return newKillSwitchChecks(
			service.SeverityWarning, "persisted kill switch is recorded; cannot inspect the kernel ("+err.Error()+")"+suffix,
		)
	case running && loaded:
		return newKillSwitchChecks(
			service.SeverityOK, "persisted kill switch is in the kernel and the daemon is running"+suffix,
		)
	case running:
		return newKillSwitchChecks(
			service.SeverityError,
			"persisted kill switch is recorded but not in the kernel while the daemon runs: its re-apply failed or "+
				"the table was deleted by hand; StopSession releases the row, a restart re-applies it",
		)
	case loaded:
		return newKillSwitchChecks(
			service.SeverityOK, "persisted kill switch is in the kernel from the last run; the next run re-applies it"+suffix,
		)
	default:
		return newKillSwitchChecks(
			service.SeverityOK,
			"persisted kill switch is recorded but not in the kernel (a reboot clears it); the next run re-applies it"+suffix,
		)
	}
}

func doctorChecks(ctx context.Context, loaded *config.Loaded, host serviceHost) []service.PreflightCheck {
	if host.goos != "linux" && host.goos != "darwin" {
		return nil
	}

	checks := doctorPermissionChecks(loaded)
	if loaded.File.Metrics.Enabled {
		others, unreadable := loadInstalledMetrics(ctx, host, loaded.Tag)
		checks = append(checks, checkMetricsListen(loaded.File.Metrics, others, unreadable))
	}

	if _, ok := loaded.Services[wireguard.Name]; ok && host.goos == "linux" {
		check, enforced := checkWGProfile(ctx, host)
		checks = append(checks, check)

		if enforced {
			checks = append(checks, inspectConfinement(ctx, host, loaded.Tag)...)
		}
	}

	if host.goos == "linux" {
		checks = append(checks, checkServiceInstall(ctx, host, loaded)...)
	}

	if host.goos == "darwin" {
		checks = append(checks, checkBackground(ctx, host, loaded.Tag)...)
	}

	return checks
}

func doctorPermissionChecks(loaded *config.Loaded) []service.PreflightCheck {
	if config.AccessGroup(loaded.Tag, loaded.Dir) == "" {
		return []service.PreflightCheck{checkInstancePermissions(loaded.Dir, -1)}
	}

	group, err := user.LookupGroup(instance.GroupName)
	if err != nil {
		return []service.PreflightCheck{service.NewPreflightCheck(
			permissionsCheckName,
			service.SeverityWarning,
			"group "+instance.GroupName+" is missing ("+err.Error()+")"+reinstallHint(loaded.Tag),
		)}
	}

	gid, _ := strconv.Atoi(group.Gid)

	return []service.PreflightCheck{checkInstancePermissions(loaded.Dir, gid)}
}

func escapeControl(text string) string {
	for i, r := range text {
		if _, size := utf8.DecodeRuneInString(text[i:]); !strconv.IsPrint(r) || (r == utf8.RuneError && size == 1) {
			quoted := strconv.Quote(text)

			return quoted[1 : len(quoted)-1]
		}
	}

	return text
}

func expectedMode(info fs.FileInfo, root bool) (fs.FileMode, bool) {
	switch {
	case root:
		return instanceDirMode, true
	case info.IsDir():
		return privateDirMode, true
	case info.Mode().IsRegular():
		return privateFileMode, true
	case info.Mode()&fs.ModeSocket != 0:
		return socketMode, true
	default:
		return 0, false
	}
}

func loadInstalledMetrics(
	ctx context.Context,
	host serviceHost,
	tag instance.Tag,
) (map[instance.Tag]config.Metrics, []string) {
	m, err := newServiceManager(host)
	if err != nil {
		return nil, []string{err.Error()}
	}

	tags, err := findInstalledOthers(ctx, host, m, tag)
	if err != nil {
		return nil, []string{err.Error()}
	}

	var unreadable []string

	others := make(map[instance.Tag]config.Metrics, len(tags))

	for _, other := range tags {
		loaded, err := config.Load(other, "", nil)
		if err != nil {
			unreadable = append(unreadable, fmt.Sprintf("%s: %v", other, err))

			continue
		}

		others[other] = loaded.File.Metrics
	}

	return others, unreadable
}

func metricsOverlap(a, b string) bool {
	hostA, portA, splitErrA := net.SplitHostPort(a)
	hostB, portB, splitErrB := net.SplitHostPort(b)

	if splitErrA != nil || splitErrB != nil {
		return a == b
	}

	wildcard := func(host string) bool {
		addr, err := netip.ParseAddr(host)

		return host == "" || (err == nil && addr.IsUnspecified())
	}

	return portA == portB && (hostA == hostB || wildcard(hostA) || wildcard(hostB))
}

func newDoctorCmd(opts *instanceOptions, probe killSwitchProbe, host serviceHost) *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Args:  cobra.NoArgs,
		Short: "run the preflight checks without running the daemon",
		Use:   "doctor",
	}
	config.AddFlags(cmd.Flags())
	cmd.Flags().StringVar(&output, "output", formatJSON, "output format: json or table")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if err := validateOutput(output); err != nil {
			return err
		}

		loaded, err := opts.load(cmd.Flags())
		if errors.Is(err, config.ErrRoleMismatch) {
			roleCheck := service.NewPreflightCheck(roleCheckName, service.SeverityError, err.Error())
			if writeErr := writeChecks(cmd.OutOrStdout(), output, []service.PreflightCheck{roleCheck}); writeErr != nil {
				return writeErr
			}

			return fmt.Errorf("doctor: %w: %s", preflight.ErrPreflightFailed, roleCheckName)
		}

		if err != nil {
			return err
		}

		log := cli.NewLogger(cmd.ErrOrStderr(), loaded.File.Log)

		rpcClient, extra, err := newPreflightRPC(cmd.Context(), loaded, probe)
		if err != nil {
			return err
		}

		extra = append(extra, doctorChecks(cmd.Context(), loaded, host)...)

		checks, preflightErr := preflight.Run(cmd.Context(), loaded, log, rpcClient, extra...)
		if err := writeChecks(cmd.OutOrStdout(), output, checks); err != nil {
			return err
		}

		if errors.Is(preflightErr, preflight.ErrPreflightFailed) {
			return fmt.Errorf("doctor: %w", preflightErr)
		}

		return preflightErr
	}

	return cmd
}

func newKillSwitchChecks(severity service.Severity, detail string) []service.PreflightCheck {
	return []service.PreflightCheck{service.NewPreflightCheck(killSwitchCheckName, severity, detail)}
}

func newPreflightRPC(
	ctx context.Context, loaded *config.Loaded, probe killSwitchProbe,
) (*rpc.Client, []service.PreflightCheck, error) {
	unpinned := loaded.File.RPC.NewClient()
	if loaded.Role != config.RoleClient {
		return unpinned, nil, nil
	}

	path := filepath.Join(loaded.Dir, storeFileName)

	if _, err := os.Stat(path); err != nil {
		return unpinned, newKillSwitchChecks(service.SeverityOK, "no persisted kill switch"), nil
	}

	db, err := store.OpenReadOnly(path)
	if err != nil {
		return unpinned, newKillSwitchChecks(
			service.SeverityWarning, "store unusable: "+err.Error()+"; checks run unpinned",
		), nil
	}
	defer func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}()

	pin, err := rpcpin.New(loaded.File.RPC.URL, loaded.File.RPC.Dialer())
	if err != nil {
		return nil, nil, fmt.Errorf("doctor: %w", err)
	}

	restored, err := client.RestorePin(ctx, db, loaded.File.RPC, pin)
	switch {
	case err != nil:
		return unpinned, newKillSwitchChecks(service.SeverityWarning, err.Error()+"; checks run unpinned"), nil
	case restored.Persistent:
		return loaded.File.RPC.NewClient(api.WithDialContext(pin.DialContext)),
			checkPersistedKillSwitch(ctx, loaded.Dir, loaded.Tag.ObjectName(), probe), nil
	case restored.Leftover:
		return unpinned, checkLeftoverKillSwitch(loaded.Dir, probe), nil
	default:
		return unpinned, newKillSwitchChecks(service.SeverityOK, "no persisted kill switch"), nil
	}
}

func validateOutput(format string) error {
	if format != formatJSON && format != formatTable {
		return fmt.Errorf("%w: %s", errInvalidOutput, format)
	}

	return nil
}

func writeChecks(w io.Writer, format string, checks []service.PreflightCheck) error {
	if err := validateOutput(format); err != nil {
		return err
	}

	if format == formatJSON {
		if err := json.NewEncoder(w).Encode(checks); err != nil {
			return fmt.Errorf("encoding checks: %w", err)
		}

		return nil
	}

	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "SEVERITY\tCHECK\tDETAIL")

	for _, check := range checks {
		_, _ = fmt.Fprintf(table, "%s\t%s\t%s\n", check.Severity, escapeControl(check.Name), escapeControl(check.Detail))
	}

	if err := table.Flush(); err != nil {
		return fmt.Errorf("writing checks: %w", err)
	}

	return nil
}
