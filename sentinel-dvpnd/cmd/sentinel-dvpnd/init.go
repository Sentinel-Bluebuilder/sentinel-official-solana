package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/spf13/cobra"

	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/daemon/instance"
	"github.com/sentinel-official/sentinel-go-sdk/v3/key"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
	"github.com/sentinel-official/sentinel-go-sdk/v3/validate"
	"github.com/sentinel-official/sentinel-go-sdk/v3/version"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const defaultAPIPort = 8443

var errInstanceExists = errors.New("instance already initialized; use --force to overwrite")

type initOptions struct {
	apiHosts         []string
	apiPort          uint16
	country          string
	force            bool
	newKey           bool
	owner            string
	peerAllowPrivate bool
	plainKey         bool
	prices           dvpn.NodePrices
	serviceName      string
}

type initResult struct {
	APIPort   uint16   `json:"api_port,omitempty"`
	Dir       string   `json:"dir"`
	ID        string   `json:"id,omitempty"`
	KeyName   string   `json:"key_name"`
	PublicKey string   `json:"public_key"`
	Reset     []string `json:"reset,omitempty"`
	Role      string   `json:"role"`
	Tag       string   `json:"tag"`
	TLSSHA256 string   `json:"tls_sha256,omitempty"`
}

func backupConfig(path string) (bool, error) {
	raw, err := os.ReadFile(path) //#nosec G304
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("reading config: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-backup-*")
	if err != nil {
		return false, fmt.Errorf("creating backup: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()

		return false, fmt.Errorf("writing backup: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return false, fmt.Errorf("closing backup: %w", err)
	}

	if err := os.Rename(tmp.Name(), path+".bak"); err != nil {
		return false, fmt.Errorf("installing backup: %w", err)
	}

	return true, nil
}

func checkInitOptions(role string, options initOptions) error {
	if !config.RoleSupported(role) {
		return fmt.Errorf("%w: %s", config.ErrUnsupportedRole, role)
	}

	if _, err := solana.PublicKeyFromBase58(options.owner); err != nil {
		return fmt.Errorf("owner: %w", err)
	}

	if role != config.RoleNode && role != config.RoleProvider {
		return nil
	}

	if err := config.ValidateAPIHosts(role, options.apiHosts); err != nil {
		return fmt.Errorf("api-hosts: %w", err)
	}

	return nil
}

func newInitCmd(opts *instanceOptions) *cobra.Command {
	var o initOptions

	cmd := &cobra.Command{
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tag, dir, err := opts.resolve()
			if err != nil {
				return err
			}

			result, err := runInit(cmd.Context(), tag, dir, o)
			if err != nil {
				return err
			}

			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
				return fmt.Errorf("encoding result: %w", err)
			}

			return nil
		},
		Short: "create the instance folder, config and operator key",
		Use:   "init",
	}
	cmd.Flags().StringVar(&o.owner, "owner", "", "owner public key (base58)")
	cmd.Flags().StringVar(&o.country, "country", "", "two-letter country code (node)")
	cmd.Flags().StringSliceVar(&o.apiHosts, "api-hosts", nil, "public api hosts (node, provider)")
	cmd.Flags().Uint16Var(
		&o.apiPort, "api-port", 0,
		"public api port (node, provider); 0 picks a free port for a node and keeps 8443 for a provider",
	)
	cmd.Flags().StringVar(
		&o.serviceName, "service", "wireguard", "service driver the node serves; init saves every driver config",
	)
	cmd.Flags().Uint64Var(
		&o.prices.ChannelPricePerGB, "prices.channel-per-gb", 0,
		"per-gb channel price in base units, as for sentinel-dvpn node open-node (node)",
	)
	cmd.Flags().Uint64Var(
		&o.prices.ChannelPricePerHour, "prices.channel-per-hour", 0,
		"per-hour channel price in base units, as for sentinel-dvpn node open-node (node)",
	)
	cmd.Flags().Uint64Var(
		&o.prices.LeasePricePerHour, "prices.lease-per-hour", 0,
		"per-hour lease price in base units, as for sentinel-dvpn node open-node (node)",
	)
	cmd.Flags().BoolVar(
		&o.peerAllowPrivate, "peer-allow-private", false, "allow node and provider endpoints on private addresses",
	)
	cmd.Flags().BoolVar(&o.plainKey, "plain-key", false, "write an unencrypted key file")
	cmd.Flags().BoolVar(&o.force, "force", false, "overwrite an existing instance")
	cmd.Flags().BoolVar(&o.newKey, "new-key", false, "replace the existing operator key")
	_ = cmd.MarkFlagRequired("owner")

	return cmd
}

func newNodeConfig(options initOptions, result *initResult) (config.Node, error) {
	node := config.DefaultNode()
	node.Owner = options.owner
	node.Country = options.country
	node.API.Hosts = options.apiHosts
	node.Service = options.serviceName
	node.Prices.ChannelPerGB = options.prices.ChannelPricePerGB
	node.Prices.ChannelPerHour = options.prices.ChannelPricePerHour
	node.Prices.LeasePerHour = options.prices.LeasePricePerHour
	node.ID = randomID()

	node.API.Port = options.apiPort
	if node.API.Port == 0 {
		port, err := service.FreePort()
		if err != nil {
			node.API.Port = defaultAPIPort
		} else {
			node.API.Port = port
		}
	}

	if _, err := service.ParseName(options.serviceName); err != nil {
		return config.Node{}, fmt.Errorf("service: %w", err)
	}

	result.KeyName, result.ID, result.APIPort = node.Key.Name, node.ID, node.API.Port

	return node, nil
}

func newRoleConfig(role string, options initOptions, result *initResult) (any, error) {
	switch role {
	case config.RoleClient:
		client := config.DefaultClient()

		client.Owner = options.owner
		for _, name := range service.Names() {
			client.Services = append(client.Services, name.String())
		}

		result.KeyName = client.Key.Name

		return client, nil
	case config.RoleNode:
		return newNodeConfig(options, result)
	case config.RoleProvider:
		provider := config.DefaultProvider()
		provider.Owner = options.owner
		provider.API.Hosts = options.apiHosts

		provider.ID = randomID()
		if options.apiPort != 0 {
			provider.API.Port = options.apiPort
		}

		result.KeyName, result.ID, result.APIPort = provider.Key.Name, provider.ID, provider.API.Port

		return provider, nil
	default:
		return nil, config.ErrInvalidRole
	}
}

func passphraseFromEnvOrPrompt(name string, confirm bool) (string, error) {
	if passphrase := os.Getenv(config.PassphraseEnv); passphrase != "" {
		return passphrase, nil
	}

	passphrase, err := cli.ReadPassphrase("passphrase for the "+name+" key", confirm)
	if err != nil {
		return "", fmt.Errorf("passphrase: %w", err)
	}

	return passphrase, nil
}

func prepareKey(dir, name string, plainKey, newKey bool) (solana.PublicKey, func() error, error) {
	path, err := cli.KeyPath(filepath.Join(dir, keysDirName), name)
	if err != nil {
		return solana.PublicKey{}, nil, fmt.Errorf("key path: %w", err)
	}

	if !newKey {
		privateKey, err := key.Load(path, "")
		if err == nil {
			return privateKey.PublicKey(), nil, nil
		}

		if errors.Is(err, key.ErrKeyLocked) {
			passphrase, err := passphraseFromEnvOrPrompt(name, false)
			if err != nil {
				return solana.PublicKey{}, nil, err
			}

			privateKey, err = key.Load(path, passphrase)
			if err != nil {
				return solana.PublicKey{}, nil, fmt.Errorf("loading key: %w", err)
			}

			return privateKey.PublicKey(), nil, nil
		}

		if !errors.Is(err, os.ErrNotExist) {
			return solana.PublicKey{}, nil, fmt.Errorf("loading key: %w", err)
		}
	}

	privateKey, err := solana.NewRandomPrivateKey()
	if err != nil {
		return solana.PublicKey{}, nil, fmt.Errorf("generating key: %w", err)
	}

	passphrase := ""
	if !plainKey {
		passphrase, err = passphraseFromEnvOrPrompt(name, true)
		if err != nil {
			return solana.PublicKey{}, nil, err
		}
	}

	return privateKey.PublicKey(), func() error { return key.Save(path, privateKey, passphrase) }, nil
}

func randomID() string { return service.RandomHex(32) }

func runInit(ctx context.Context, tag instance.Tag, dir string, options initOptions) (initResult, error) {
	if _, err := os.Stat(filepath.Join(dir, config.FileName)); err == nil && !options.force {
		return initResult{}, errInstanceExists
	}

	if _, err := config.Load(tag, dir, nil); errors.Is(err, config.ErrRoleMismatch) {
		return initResult{}, err
	}

	role := tag.Role()
	if err := checkInitOptions(role, options); err != nil {
		return initResult{}, err
	}

	file := config.DefaultFile(version.Network)
	file.Daemon.Role = role
	file.Peer.AllowPrivate = options.peerAllowPrivate

	result := initResult{
		Dir:  dir,
		Role: role,
		Tag:  string(tag),
	}

	roleCfg, err := newRoleConfig(role, options, &result)
	if err != nil {
		return initResult{}, err
	}

	if err := validateRoleConfig(roleCfg); err != nil {
		return initResult{}, err
	}

	group := config.AccessGroup(tag, dir)
	if group != "" {
		if err := ensureGroup(ctx, runtime.GOOS, service.Run); err != nil {
			return initResult{}, err
		}
	}

	if err := prepareInstanceDir(dir, group); err != nil {
		return initResult{}, err
	}

	if err := os.MkdirAll(filepath.Join(dir, keysDirName), privateDirMode); err != nil {
		return initResult{}, fmt.Errorf("creating keys folder: %w", err)
	}

	publicKey, saveKey, err := prepareKey(dir, result.KeyName, options.plainKey, options.newKey)
	if err != nil {
		return initResult{}, err
	}

	result.PublicKey = publicKey.String()

	if !options.force && saveKey != nil {
		if err := saveKey(); err != nil {
			return initResult{}, fmt.Errorf("saving key: %w", err)
		}

		saveKey = nil
	}

	if role != config.RoleClient {
		_, fingerprint, err := api.LoadOrCreateServerCert(dir)
		if err != nil {
			return initResult{}, fmt.Errorf("tls certificate: %w", err)
		}

		result.TLSSHA256 = fingerprint
	}

	if err := saveInstanceConfigs(dir, file, roleCfg, &result); err != nil {
		return initResult{}, fmt.Errorf("saving config: %w", err)
	}

	if saveKey != nil {
		if err := saveKey(); err != nil {
			return initResult{}, fmt.Errorf("saving key: %w", err)
		}
	}

	return result, nil
}

func saveInstanceConfigs(dir string, file config.File, roleCfg any, result *initResult) error {
	for _, path := range config.Paths(file.Daemon.Role) {
		backedUp, err := backupConfig(filepath.Join(dir, path))
		if err != nil {
			return fmt.Errorf("backing up %s: %w", path, err)
		}

		if backedUp {
			result.Reset = append(result.Reset, path)
		}
	}

	if err := config.Save(dir, file, roleCfg); err != nil {
		return err
	}

	if file.Daemon.Role == config.RoleProvider {
		return nil
	}

	for _, name := range service.Names() {
		var cfg any

		var err error
		if file.Daemon.Role == config.RoleNode {
			cfg, err = service.DefaultServerConfig(name)
			if err != nil {
				return fmt.Errorf("resolving default server config for %s: %w", name, err)
			}
		} else {
			cfg, err = service.DefaultClientConfig(name)
			if err != nil {
				return fmt.Errorf("resolving default client config for %s: %w", name, err)
			}
		}

		if err := config.SaveService(dir, name, cfg); err != nil {
			return fmt.Errorf("saving %s service config: %w", name, err)
		}
	}

	return nil
}

func validateRoleConfig(roleCfg any) error {
	if err := validate.Struct(roleCfg); err != nil {
		return err
	}

	if cfg, ok := roleCfg.(interface{ Validate() error }); ok {
		return cfg.Validate()
	}

	return nil
}
