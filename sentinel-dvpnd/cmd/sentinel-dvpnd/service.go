package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
)

var (
	errInvalidDir      = errors.New("path exists and is not a directory")
	errUnsupportedHome = errors.New(
		"--home is unsupported by service commands; the service manager runs the instance from its state folder",
	)
	errUnsupportedOS = errors.New("unsupported operating system")
)

var scmServiceManager func(host serviceHost) serviceManager //nolint:gochecknoglobals

type serviceManager interface {
	Install(ctx context.Context, loaded *config.Loaded) error
	Installed(ctx context.Context, tag instance.Tag) (bool, error)
	Prune(ctx context.Context) error
	Restart(ctx context.Context, tag instance.Tag) error
	Start(ctx context.Context, tag instance.Tag) error
	Status(ctx context.Context, tag instance.Tag) (string, error)
	Stop(ctx context.Context, tag instance.Tag) error
	Uninstall(ctx context.Context, tag instance.Tag) error
}

func newServiceManager(host serviceHost) (serviceManager, error) {
	switch host.goos {
	case "darwin":
		return launchdManager{host: host}, nil
	case "linux":
		return systemdManager{host: host}, nil
	case "windows":
		if scmServiceManager != nil {
			return scmServiceManager(host), nil
		}
	}

	return nil, fmt.Errorf("%w: %s", errUnsupportedOS, host.goos)
}

func canonicalExecutable(path string) string {
	const shortName = "dvpnd"

	ext := filepath.Ext(path)
	if strings.TrimSuffix(filepath.Base(path), ext) != shortName {
		return path
	}

	canonical := filepath.Join(filepath.Dir(path), instance.DaemonName+ext)
	if _, err := os.Stat(canonical); err != nil {
		return path
	}

	return canonical
}

func findInstalledOthers(
	ctx context.Context,
	host serviceHost,
	m serviceManager,
	tag instance.Tag,
) ([]instance.Tag, error) {
	tags, err := instance.List(filepath.Join(host.root, instance.StateRoot(host.goos)))
	if err != nil {
		return nil, err
	}

	var installed []instance.Tag

	for _, other := range tags {
		if other == tag {
			continue
		}

		ok, err := m.Installed(ctx, other)
		if err != nil {
			return nil, err
		}

		if ok {
			installed = append(installed, other)
		}
	}

	return installed, nil
}

func installService(
	ctx context.Context,
	host serviceHost,
	m serviceManager,
	tag instance.Tag,
	home string,
	out io.Writer,
) error {
	loaded, err := config.Load(tag, home, nil)
	if err != nil {
		return err
	}

	if err := ensureGroup(ctx, host.goos, host.exec); err != nil {
		return err
	}

	if err := prepareInstanceDir(loaded.Dir, config.AccessGroup(tag, loaded.Dir)); err != nil {
		return err
	}

	if err := m.Install(ctx, loaded); err != nil {
		return err
	}

	for _, hint := range serviceHints(host.goos, tag) {
		_, _ = fmt.Fprintln(out, hint)
	}

	return nil
}

func newServiceActionCmd(
	opts *instanceOptions,
	host serviceHost,
	action func(cmd *cobra.Command, m serviceManager, tag instance.Tag) error,
	use, short string,
) *cobra.Command {
	return &cobra.Command{
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if opts.home != "" {
				return errUnsupportedHome
			}

			tag, err := opts.parse()
			if err != nil {
				return err
			}

			m, err := newServiceManager(host)
			if err != nil {
				return err
			}

			return action(cmd, m, tag)
		},
		Short: short,
		Use:   use,
	}
}

func newServiceCmd(opts *instanceOptions, host serviceHost) *cobra.Command {
	install := func(cmd *cobra.Command, m serviceManager, tag instance.Tag) error {
		return installService(cmd.Context(), host, m, tag, opts.home, cmd.OutOrStdout())
	}
	restart := func(cmd *cobra.Command, m serviceManager, tag instance.Tag) error {
		return m.Restart(cmd.Context(), tag)
	}
	start := func(cmd *cobra.Command, m serviceManager, tag instance.Tag) error {
		return m.Start(cmd.Context(), tag)
	}
	status := func(cmd *cobra.Command, m serviceManager, tag instance.Tag) error {
		state := "not installed"

		installed, err := m.Installed(cmd.Context(), tag)
		if err == nil && installed {
			state, err = m.Status(cmd.Context(), tag)
		}

		if err != nil {
			return err
		}

		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", tag.ServiceName(host.goos), state)

		return nil
	}
	stop := func(cmd *cobra.Command, m serviceManager, tag instance.Tag) error {
		return m.Stop(cmd.Context(), tag)
	}
	uninstall := func(cmd *cobra.Command, m serviceManager, tag instance.Tag) error {
		return uninstallService(cmd.Context(), host, m, tag)
	}

	cmd := &cobra.Command{
		Example: "  sentinel-dvpnd service install --instance c0\n" +
			"  sentinel-dvpnd service start --instance c0\n" +
			"  sentinel-dvpnd service stop --instance c0\n" +
			"  sentinel-dvpnd service restart --instance c0\n" +
			"  sentinel-dvpnd service status --instance c0\n" +
			"  sentinel-dvpnd service uninstall --instance c0",
		Long: "Manage the instance's OS service. Install registers the initialized instance with the OS service " +
			"manager and does not start it; start, stop, restart and status act on the registered service; " +
			"uninstall stops and removes it. " +
			"Keep the executable at its installed location; rerun install after moving it. " +
			"Encrypted keys require passphrase_file readable by the service; interactive prompts are unavailable.",
		Short: "manage the instance's os service (root or administrator required)",
		Use:   "service",
	}
	cmd.AddCommand(
		newServiceActionCmd(opts, host, install, "install", "register the initialized instance as an automatic service"),
		newServiceActionCmd(opts, host, restart, "restart", "restart the instance's service"),
		newServiceActionCmd(opts, host, start, "start", "start the instance's service"),
		newServiceActionCmd(opts, host, status, "status", "print the instance's service state"),
		newServiceActionCmd(opts, host, stop, "stop", "stop the instance's service"),
		newServiceActionCmd(opts, host, uninstall, "uninstall", "stop and remove the instance's service"),
	)

	return cmd
}

func realExecutable() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating executable: %w", err)
	}

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolving executable: %w", err)
	}

	return canonicalExecutable(resolved), nil
}

func serviceHints(goos string, tag instance.Tag) []string {
	const grant = "grant a user access (effective at next login): "

	name := tag.ServiceName(goos)
	hints := []string{"installed " + name + " (not started)"}

	switch goos {
	case "darwin":
		return append(hints,
			"start: launchctl bootstrap system "+launchDaemonsDir+"/"+name+".plist (when not loaded), "+
				"then launchctl kickstart system/"+name,
			"stop: launchctl bootout system/"+name,
			grant+"dseditgroup -o edit -a <user> -t user "+instance.GroupName,
		)
	case "windows":
		return append(hints,
			"start: sc.exe start '"+name+"'",
			"stop: sc.exe stop '"+name+"'",
			grant+"net localgroup "+instance.GroupName+" <user> /add",
		)
	default:
		return append(hints,
			"start: systemctl start "+name,
			"stop: systemctl stop "+name+".socket "+name+".service",
			grant+"usermod -aG "+instance.GroupName+" <user>",
		)
	}
}

func uninstallService(ctx context.Context, host serviceHost, m serviceManager, tag instance.Tag) error {
	if err := m.Uninstall(ctx, tag); err != nil {
		return err
	}

	others, err := findInstalledOthers(ctx, host, m, tag)
	if err != nil {
		return err
	}

	if len(others) > 0 {
		return nil
	}

	if err := m.Prune(ctx); err != nil {
		return err
	}

	return deleteGroup(ctx, host.goos, host.exec)
}

func writeSystemFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("creating system file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("setting system file mode: %w", err)
	}

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("writing system file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing system file: %w", err)
	}

	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("installing system file: %w", err)
	}

	return nil
}
