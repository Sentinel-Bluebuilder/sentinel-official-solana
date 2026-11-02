package openvpn

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

const (
	dcoHardwareID  = "ovpn-dco"
	tapctlBinary   = "tapctl.exe"
	tapctlTimeout  = 10 * time.Second
	versionTimeout = 5 * time.Second
)

//nolint:gochecknoglobals
var (
	minVersion     = [3]int{2, 7, 8}
	minVersionText = fmt.Sprintf("%d.%d.%d", minVersion[0], minVersion[1], minVersion[2])
	detectDCO      = func() (bool, error) { return false, errors.ErrUnsupported }
	versionPattern = regexp.MustCompile(`OpenVPN (\d+)\.(\d+)\.(\d+)`)
)

type execFunc func(ctx context.Context, c service.Command) ([]byte, error)

type tapctl struct {
	exec           execFunc
	interfaceNames func() (map[string]bool, error)
	lookPath       func(name string) (string, error)
}

func newTapctl() *tapctl {
	return &tapctl{
		exec:           service.Run,
		interfaceNames: service.InterfaceNames,
		lookPath:       service.LookPath,
	}
}

func (t tapctl) Create(ctx context.Context, name string) error {
	err := t.run(ctx, "create", "--hwid", dcoHardwareID, "--name", name)
	if err == nil {
		return nil
	}

	msg := err.Error()

	switch {
	case strings.Contains(msg, "already exists"), strings.Contains(msg, "is already in use"):
		return fmt.Errorf("adapter %s is still registered; delete it or reboot: %w", name, err)
	case strings.Contains(msg, "Renaming "):
		return fmt.Errorf("renaming adapter %s failed; tapctl deleted it (OpenVPN issue #544): %w", name, err)
	default:
		return fmt.Errorf("tapctl create %s: %w", name, err)
	}
}

func (t tapctl) Delete(ctx context.Context, name string) error {
	if err := t.run(ctx, "delete", name); err != nil {
		return fmt.Errorf("tapctl delete %s: %w", name, err)
	}

	interfaceNames, err := t.interfaceNames()
	if err != nil {
		return fmt.Errorf("checking adapter %s: %w", name, err)
	}

	if interfaceNames[name] {
		return fmt.Errorf("adapter %s is still present after delete; reboot to remove it", name)
	}

	return nil
}

func (t tapctl) run(ctx context.Context, args ...string) error {
	openvpnPath, err := t.lookPath(binary)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, tapctlTimeout)
	defer cancel()

	dir := filepath.Dir(openvpnPath)
	_, err = t.exec(ctx, service.Command{
		Args: args,
		Dir:  dir,
		Path: filepath.Join(dir, tapctlBinary),
	})

	return err
}

func checkDCO(present bool, err error) []service.PreflightCheck {
	var detail string

	switch {
	case err != nil:
		detail = "could not check the ovpn-dco driver: " + err.Error()
	case !present:
		detail = "ovpn-dco driver not found; install OpenVPN " + minVersionText + " or later with its DCO driver"
	default:
		return nil
	}

	return []service.PreflightCheck{service.NewPreflightCheck("service_driver", service.SeverityWarning, detail)}
}

func checkVersion(out []byte) []service.PreflightCheck {
	match := versionPattern.FindSubmatch(out)
	if match == nil {
		return []service.PreflightCheck{
			service.NewPreflightCheck("service_version", service.SeverityWarning, "could not read the OpenVPN version"),
		}
	}

	got := make([]int, 0, len(minVersion))

	for _, part := range match[1:] {
		n, _ := strconv.Atoi(string(part))
		got = append(got, n)
	}

	if slices.Compare(got, minVersion[:]) >= 0 {
		return nil
	}

	detail := string(match[0]) + " found; Windows needs " + minVersionText + " or later"

	return []service.PreflightCheck{service.NewPreflightCheck("service_version", service.SeverityWarning, detail)}
}

func preflightDCO(ctx context.Context, goos string) []service.PreflightCheck {
	if goos != "windows" {
		return nil
	}

	openvpnPath, err := service.LookPath(binary)
	if err != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()

	out, _ := service.Run(ctx, service.Command{
		Args: []string{"--version"},
		Path: openvpnPath,
	})
	present, err := detectDCO()

	return slices.Concat(checkVersion(out), checkDCO(present, err))
}
