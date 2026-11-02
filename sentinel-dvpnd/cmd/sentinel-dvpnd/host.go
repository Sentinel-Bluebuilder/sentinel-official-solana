package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/wireguard"
)

const (
	apparmorProfilesPath              = "/sys/kernel/security/apparmor/profiles"
	apparmorWGCheckName               = "apparmor_wg"
	installedCheckName                = "installed"
	kernelProbeHint                   = "cannot probe kernel support; run doctor as root: "
	kernelWireGuardCheckName          = "kernel_wireguard"
	runWireGuardCheckName             = "run_wireguard"
	toolDirsCheckName                 = "tool_dirs"
	userspaceDropInCheckName          = "userspace_drop_in"
	userspaceDropInWireGuardCheckName = "userspace_drop_in_wireguard"
	wgProbeFilePattern                = "sentinel-dvpn-probe-*.conf"
	wireGuardRunDir                   = "/run/wireguard"
)

type execFunc func(ctx context.Context, c service.Command) ([]byte, error)

type kernelSupportFunc func(ctx context.Context, kind string) (bool, error)

type serviceHost struct {
	exec          execFunc
	executable    func() (string, error)
	goos          string
	kernelSupport kernelSupportFunc
	root          string
}

func newServiceHost() serviceHost {
	return serviceHost{
		exec:          service.Run,
		executable:    realExecutable,
		goos:          runtime.GOOS,
		kernelSupport: service.DetectKernelSupport,
	}
}

func (h serviceHost) dropIn(tag instance.Tag) (string, error) {
	path := filepath.Join(h.root, userspaceDropInPath(tag))

	data, err := os.ReadFile(path) //#nosec G304 -- fixed path under the root seam
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("cannot read the userspace drop-in: %w", err)
	}

	return string(data), nil
}

func (h serviceHost) profiles() string {
	data, _ := os.ReadFile(filepath.Join(h.root, apparmorProfilesPath)) //#nosec G304 -- fixed path under the root seam

	return string(data)
}

func (h serviceHost) wgEnforced(ctx context.Context) (bool, error) {
	if profiles := h.profiles(); profiles != "" {
		return slices.Contains(strings.Split(profiles, "\n"), "wg (enforce)"), nil
	}

	file, err := os.CreateTemp("", wgProbeFilePattern)
	if err != nil {
		return false, fmt.Errorf("creating wg probe file: %w", err)
	}

	defer func() { _ = os.Remove(file.Name()) }()

	if err := file.Close(); err != nil {
		return false, fmt.Errorf("closing wg probe file: %w", err)
	}

	_, err = h.exec(ctx, service.Command{
		Args: []string{"setconf", "lo", file.Name()},
		Path: "wg",
	})
	if errors.Is(err, exec.ErrNotFound) {
		return false, fmt.Errorf("probing wg: %w", err)
	}

	return err != nil && strings.Contains(err.Error(), "fopen: Permission denied"), nil
}

func checkDropIn(ctx context.Context, host serviceHost, loaded *config.Loaded) service.PreflightCheck {
	names, err := userspaceNames(ctx, loaded, host.kernelSupport)
	if err != nil {
		return service.NewPreflightCheck(userspaceDropInCheckName, service.SeverityWarning, kernelProbeHint+err.Error())
	}

	var want string
	if len(names) > 0 {
		want = renderUserspaceDropIn(names)
	}

	got, err := host.dropIn(loaded.Tag)
	if err != nil {
		return service.NewPreflightCheck(userspaceDropInCheckName, service.SeverityWarning, err.Error())
	}

	hint := reinstallHint(loaded.Tag)

	switch {
	case got == want:
		return service.NewPreflightCheck(
			userspaceDropInCheckName, service.SeverityOK, "the userspace drop-in matches the kernel support",
		)
	case got == "":
		return service.NewPreflightCheck(userspaceDropInCheckName, service.SeverityError,
			"the userspace drop-in is missing; the userspace tool cannot create its runtime folder"+hint)
	case want == "":
		return service.NewPreflightCheck(
			userspaceDropInCheckName, service.SeverityWarning, "the userspace drop-in is stale: the kernel has support"+hint,
		)
	default:
		return service.NewPreflightCheck(
			userspaceDropInCheckName, service.SeverityError, "the userspace drop-in is out of date"+hint,
		)
	}
}

func checkServiceInstall(ctx context.Context, host serviceHost, loaded *config.Loaded) []service.PreflightCheck {
	installed, err := detectInstalled(ctx, host, loaded.Tag)
	if err != nil {
		return []service.PreflightCheck{service.NewPreflightCheck(
			installedCheckName, service.SeverityWarning, "cannot tell whether the service is installed: "+err.Error(),
		)}
	}

	if !installed {
		return nil
	}

	return []service.PreflightCheck{checkToolDirs(host, loaded), checkDropIn(ctx, host, loaded)}
}

func checkToolDirs(host serviceHost, loaded *config.Loaded) service.PreflightCheck {
	var missing, unreadable []string

	for _, dir := range instanceToolDirs(loaded) {
		info, err := os.Stat(filepath.Join(host.root, dir))

		switch {
		case errors.Is(err, fs.ErrNotExist) || (err == nil && !info.IsDir()):
			missing = append(missing, dir)
		case err != nil:
			unreadable = append(unreadable, "cannot inspect "+dir+": "+err.Error())
		}
	}

	switch {
	case len(missing) > 0:
		return service.NewPreflightCheck(toolDirsCheckName, service.SeverityError,
			"tool folders are missing: "+strings.Join(missing, ", ")+reinstallHint(loaded.Tag))
	case len(unreadable) > 0:
		return service.NewPreflightCheck(
			toolDirsCheckName, service.SeverityWarning, strings.Join(unreadable, "; ")+"; run doctor as root",
		)
	default:
		return service.NewPreflightCheck(toolDirsCheckName, service.SeverityOK, "the tool folders exist")
	}
}

func checkWGProfile(ctx context.Context, host serviceHost) (service.PreflightCheck, bool) {
	enforced, err := host.wgEnforced(ctx)

	switch {
	case err != nil:
		return service.NewPreflightCheck(
			apparmorWGCheckName,
			service.SeverityWarning,
			"cannot tell whether the wg AppArmor profile is enforced: "+err.Error(),
		), false
	case !enforced:
		return service.NewPreflightCheck(
			apparmorWGCheckName, service.SeverityOK, "the wg AppArmor profile is not enforced",
		), false
	case host.profiles() != "":
		return service.NewPreflightCheck(
			apparmorWGCheckName, service.SeverityOK, "the wg AppArmor profile is enforced (securityfs)",
		), true
	default:
		return service.NewPreflightCheck(
			apparmorWGCheckName, service.SeverityOK, "the wg AppArmor profile is enforced (probe)",
		), true
	}
}

func checkWireGuardConfinement(root string, tag instance.Tag, kernel bool, dropIn string) []service.PreflightCheck {
	var checks []service.PreflightCheck

	if !kernel {
		checks = append(checks, service.NewPreflightCheck(kernelWireGuardCheckName, service.SeverityError,
			"this host confines wg; load the WireGuard kernel module (modprobe wireguard)"))
	}

	if slices.Contains(strings.Split(dropIn, "\n"), "RuntimeDirectory="+wireguard.Name.String()) {
		checks = append(checks, service.NewPreflightCheck(userspaceDropInWireGuardCheckName, service.SeverityError,
			"the drop-in gives wireguard-go "+wireGuardRunDir+", which the enforced wg profile can't reach; "+
				"load the WireGuard kernel module"+reinstallHint(tag)))
	}

	if _, err := os.Stat(filepath.Join(root, wireGuardRunDir)); kernel && err == nil {
		checks = append(checks, service.NewPreflightCheck(runWireGuardCheckName, service.SeverityError,
			wireGuardRunDir+" exists; with the enforced wg profile wg-quick down can't list interfaces and leaks "+
				tag.Interface()+"; remove it by hand once no wireguard-go runs (rmdir "+wireGuardRunDir+")"))
	}

	return checks
}

func detectInstalled(ctx context.Context, host serviceHost, tag instance.Tag) (bool, error) {
	m, err := newServiceManager(host)
	if err != nil {
		return false, err
	}

	return m.Installed(ctx, tag)
}

func inspectConfinement(ctx context.Context, host serviceHost, tag instance.Tag) []service.PreflightCheck {
	kernel, err := host.kernelSupport(ctx, wireguard.Name.String())
	if err != nil {
		return []service.PreflightCheck{service.NewPreflightCheck(
			kernelWireGuardCheckName, service.SeverityWarning, kernelProbeHint+err.Error(),
		)}
	}

	dropIn, err := host.dropIn(tag)
	checks := checkWireGuardConfinement(host.root, tag, kernel, dropIn)

	if err != nil {
		checks = append(checks, service.NewPreflightCheck(
			userspaceDropInWireGuardCheckName, service.SeverityWarning, err.Error(),
		))
	}

	return checks
}

func reinstallHint(tag instance.Tag) string {
	return "; rerun " + instance.DaemonName + " service install --instance " + string(tag)
}
