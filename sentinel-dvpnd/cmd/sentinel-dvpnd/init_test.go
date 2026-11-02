package main_test

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"

	main "github.com/sentinel-official/sentinel-dvpnd/cmd/sentinel-dvpnd"
	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-dvpnd/internal/store"
	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
	"github.com/sentinel-official/sentinel-go-sdk/v3/key"
)

func TestRunInit_ForceBackupFailureKeepsKeyAndConfig(t *testing.T) {
	t.Setenv(config.PassphraseEnv, "secret")
	home := t.TempDir()
	args := []string{"--instance", "c0", "--owner", randomOwner(t)}
	result, err := runInitCmd(t, home, args...)
	require.NoError(t, err)

	keyPath := filepath.Join(home, "keys", result.KeyName+".json")
	keyBefore, err := os.ReadFile(keyPath) //#nosec G304
	require.NoError(t, err)

	configPath := filepath.Join(home, config.FileName)
	configBefore, err := os.ReadFile(configPath) //#nosec G304
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(configPath+".bak", 0o700))

	_, err = runInitCmd(t, home, append(args, "--force", "--new-key")...)
	require.ErrorContains(t, err, "backing up")
	keyAfter, err := os.ReadFile(keyPath) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, keyBefore, keyAfter)

	configAfter, err := os.ReadFile(configPath) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, configBefore, configAfter)
}

func TestRunInit_ForceBackupReplacesPermissiveFile(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == goosWindows {
		t.Skip("windows reports file permissions differently")
	}

	home := t.TempDir()
	args := []string{"--instance", "c0", "--owner", randomOwner(t), "--plain-key"}
	_, err := runInitCmd(t, home, args...)
	require.NoError(t, err)

	backupPath := filepath.Join(home, config.FileName+".bak")
	require.NoError(t, os.WriteFile(backupPath, []byte("old backup"), 0o644)) //#nosec G306
	require.NoError(t, os.Chmod(backupPath, 0o644))                           //#nosec G302

	_, err = runInitCmd(t, home, append(args, "--force")...)
	require.NoError(t, err)
	info, err := os.Stat(backupPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestRunInit_ForceBackupSymlinkKeepsKey(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == goosWindows {
		t.Skip("creating symlinks may require privileges on windows")
	}

	home := t.TempDir()
	args := []string{"--instance", "c0", "--owner", randomOwner(t), "--plain-key"}
	result, err := runInitCmd(t, home, args...)
	require.NoError(t, err)

	keyPath := filepath.Join(home, "keys", result.KeyName+".json")
	keyBefore, err := os.ReadFile(keyPath) //#nosec G304
	require.NoError(t, err)

	backupPath := filepath.Join(home, config.FileName+".bak")
	require.NoError(t, os.Symlink(keyPath, backupPath))

	_, err = runInitCmd(t, home, append(args, "--force")...)
	require.NoError(t, err)
	keyAfter, err := os.ReadFile(keyPath) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, keyBefore, keyAfter)

	info, err := os.Lstat(backupPath)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular())
}

func TestRunInit_ForceKeepsKeyAndBacksUpConfigs(t *testing.T) {
	t.Setenv(config.PassphraseEnv, "secret")
	home := t.TempDir()
	args := []string{"--instance", "c0", "--owner", randomOwner(t)}

	first, err := runInitCmd(t, home, args...)
	require.NoError(t, err)
	before, err := os.ReadFile(filepath.Join(home, config.FileName)) //#nosec G304
	require.NoError(t, err)

	second, err := runInitCmd(t, home, append(args, "--force")...)
	require.NoError(t, err)
	require.Equal(t, first.PublicKey, second.PublicKey)
	require.ElementsMatch(t, []string{
		"config.toml",
		"amneziawg/config.toml",
		"hysteria2/config.toml",
		"openvpn/config.toml",
		"v2ray/config.toml",
		"wireguard/config.toml",
		"xray/config.toml",
	}, second.Reset)

	for _, path := range second.Reset {
		requireFileExists(t, filepath.Join(home, path+".bak"))
	}

	backup, err := os.ReadFile(filepath.Join(home, config.FileName+".bak")) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, before, backup)

	third, err := runInitCmd(t, home, append(args, "--force", "--new-key")...)
	require.NoError(t, err)
	require.NotEqual(t, first.PublicKey, third.PublicKey)
}

func TestRunInit_ForceNewKeyPromptFailureKeepsConfig(t *testing.T) {
	t.Parallel()

	if home, ok := os.LookupEnv("SENTINEL_TEST_INIT_NEW_KEY_HOME"); ok {
		_, err := runInitCmd(t, home, "--instance", "c0", "--owner", randomOwner(t), "--force", "--new-key")
		require.Error(t, err)

		return
	}

	home := t.TempDir()
	_, err := runInitCmd(t, home, "--instance", "c0", "--owner", randomOwner(t), "--plain-key")
	require.NoError(t, err)

	configPath := filepath.Join(home, config.FileName)
	require.NoError(t, os.WriteFile(configPath, []byte("original config"), 0o600))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext( //#nosec G702
		ctx, os.Args[0], "-test.run=^TestRunInit_ForceNewKeyPromptFailureKeepsConfig$",
	)

	cmd.Env = append(os.Environ(), "SENTINEL_TEST_INIT_NEW_KEY_HOME="+home, config.PassphraseEnv+"=")
	output, err := cmd.CombinedOutput()
	require.NoError(t, ctx.Err(), string(output))
	require.NoError(t, err, string(output))
	configAfter, err := os.ReadFile(configPath) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, []byte("original config"), configAfter)
}

func TestRunInit_ForceRefusesRoleMismatch(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	_, err := runInitCmd(t, home, "--instance", "c0", "--owner", randomOwner(t), "--plain-key")
	require.NoError(t, err)

	_, err = runInitCmd(t, home, "--instance", "n0", "--owner", randomOwner(t), "--force")
	require.ErrorIs(t, err, config.ErrRoleMismatch)
}

func TestRunInit_ForceWrongPassphraseKeepsKeyAndConfig(t *testing.T) {
	t.Setenv(config.PassphraseEnv, "secret")
	home := t.TempDir()
	args := []string{"--instance", "c0", "--owner", randomOwner(t)}
	result, err := runInitCmd(t, home, args...)
	require.NoError(t, err)

	keyPath := filepath.Join(home, "keys", result.KeyName+".json")
	keyBefore, err := os.ReadFile(keyPath) //#nosec G304
	require.NoError(t, err)

	configPath := filepath.Join(home, config.FileName)
	configBefore, err := os.ReadFile(configPath) //#nosec G304
	require.NoError(t, err)
	t.Setenv(config.PassphraseEnv, "wrong")

	_, err = runInitCmd(t, home, append(args, "--force")...)
	require.ErrorIs(t, err, key.ErrInvalidPassphrase)
	keyAfter, err := os.ReadFile(keyPath) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, keyBefore, keyAfter)

	configAfter, err := os.ReadFile(configPath) //#nosec G304
	require.NoError(t, err)
	require.Equal(t, configBefore, configAfter)
}

func TestRunInit_InstanceExists(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("the node and provider roles are supported on linux only")
	}

	home := t.TempDir()
	args := []string{
		"--instance", "p0",
		"--owner", randomOwner(t),
		"--api-hosts", "203.0.113.7",
		"--plain-key",
	}
	_, err := runInitCmd(t, home, args...)
	require.NoError(t, err)
	_, err = runInitCmd(t, home, args...)
	require.ErrorIs(t, err, main.ErrInstanceExists)
}

func TestRunInit_InvalidInstance(t *testing.T) {
	t.Parallel()

	home := filepath.Join(t.TempDir(), "home")
	_, err := runInitCmd(t, home, "--instance", "x0", "--owner", randomOwner(t), "--plain-key")
	require.ErrorIs(t, err, instance.ErrInvalidTag)
	_, err = os.Stat(home)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRunInit_Node(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("the node and provider roles are supported on linux only")
	}

	home := t.TempDir()
	result, err := runInitCmd(t, home,
		"--instance", "n0",
		"--owner", randomOwner(t),
		"--country", "US",
		"--api-hosts", "203.0.113.5",
		"--service", "wireguard",
		"--plain-key",
	)
	require.NoError(t, err)
	require.Len(t, result.ID, 64)
	require.NotEmpty(t, result.TLSSHA256)
	require.NotZero(t, result.APIPort)
	requireFileExists(t, filepath.Join(home, "config.toml"))
	requireFileExists(t, filepath.Join(home, "keys", result.KeyName+".json"))
	requireFileExists(t, filepath.Join(home, "tls.crt"))
	requireFileExists(t, filepath.Join(home, "tls.key"))
	requireFileExists(t, filepath.Join(home, "wireguard", "config.toml"))
	_, err = os.Stat(filepath.Join(home, "node"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRunInit_PeerAllowPrivate(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	_, err := runInitCmd(t, home,
		"--instance", "c0",
		"--owner", randomOwner(t),
		"--peer-allow-private",
		"--plain-key",
	)
	require.NoError(t, err)

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	config.AddFlags(flags)
	require.NoError(t, flags.Set("rpc.url", "http://127.0.0.1:8899"))
	require.NoError(t, flags.Set("rpc.ws-url", "ws://127.0.0.1:8900"))

	loaded, err := config.Load("c0", home, flags)
	require.NoError(t, err)
	require.True(t, loaded.File.Peer.AllowPrivate)
}

func TestRunInit_PrivateFileModes(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == goosWindows {
		t.Skip("windows reports file permissions differently")
	}

	dir := t.TempDir()
	_, err := runInitCmd(t, dir, "--instance", "c0", "--owner", randomOwner(t), "--plain-key")
	require.NoError(t, err)

	db, err := store.Open(filepath.Join(dir, "data.db"))
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.Exec("CREATE TABLE t(x)").Error)

	require.NoError(t, filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		info, err := d.Info()
		require.NoError(t, err)

		want := os.FileMode(0o600)

		switch {
		case path == dir:
			want = 0o750
		case d.IsDir():
			want = 0o700
		}

		require.Equal(t, want, info.Mode().Perm(), path)

		return nil
	}))
}

func TestRunInit_RefusesServerRolesOffLinux(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "linux" {
		t.Skip("every role is supported on linux")
	}

	for _, tag := range []string{"n0", "p0"} {
		t.Run(tag, func(t *testing.T) {
			t.Parallel()
			home := filepath.Join(t.TempDir(), "uninitialized")
			_, err := runInitCmd(t, home, "--instance", tag, "--owner", randomOwner(t), "--plain-key")
			require.ErrorIs(t, err, config.ErrUnsupportedRole)
			_, err = os.Stat(home)
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestRunInit_RejectsNonASCIICountry(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("the node role is supported on linux only")
	}

	home := filepath.Join(t.TempDir(), "home")
	_, err := runInitCmd(t, home,
		"--instance", "n0",
		"--owner", randomOwner(t),
		"--country", "ÉÉ",
		"--api-hosts", "203.0.113.5",
		"--plain-key",
	)
	require.ErrorContains(t, err, "country")
	_, err = os.Stat(home)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRunInit_RejectsPriceAboveInt64(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("the node role is supported on linux only")
	}

	home := filepath.Join(t.TempDir(), "home")
	_, err := runInitCmd(t, home,
		"--instance", "n0",
		"--owner", randomOwner(t),
		"--country", "US",
		"--api-hosts", "203.0.113.5",
		"--prices.channel-per-gb", "9223372036854775808",
		"--plain-key",
	)
	require.ErrorContains(t, err, "channel_per_gb")
	_, err = os.Stat(filepath.Join(home, "keys"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRunInit_UsesPassphraseEnv(t *testing.T) {
	if home, ok := os.LookupEnv("SENTINEL_TEST_INIT_PASSPHRASE_HOME"); ok {
		result, err := runInitCmd(t, home, "--instance", "c0", "--owner", randomOwner(t))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(home, "key-name"), []byte(result.KeyName), 0o600))

		return
	}

	t.Setenv(config.PassphraseEnv, "secret")
	home := t.TempDir()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRunInit_UsesPassphraseEnv$") //#nosec G702

	cmd.Env = append(os.Environ(), "SENTINEL_TEST_INIT_PASSPHRASE_HOME="+home)
	output, err := cmd.CombinedOutput()
	require.NoError(t, ctx.Err(), string(output))
	require.NoError(t, err, string(output))

	name, err := os.ReadFile(filepath.Join(home, "key-name")) //#nosec G304
	require.NoError(t, err)

	path := filepath.Join(home, "keys", string(name)+".json")
	_, err = key.Load(path, "")
	require.ErrorIs(t, err, key.ErrKeyLocked)
	privateKey, err := key.Load(path, "secret")
	require.NoError(t, err)
	require.NotEmpty(t, privateKey)
}
