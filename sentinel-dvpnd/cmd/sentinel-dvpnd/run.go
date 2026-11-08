package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gorm.io/gorm"

	"github.com/sentinel-official/sentinel-dvpnd/internal/client"
	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-dvpnd/internal/daemon"
	"github.com/sentinel-official/sentinel-dvpnd/internal/preflight"
	"github.com/sentinel-official/sentinel-dvpnd/internal/rpcpin"
	"github.com/sentinel-official/sentinel-dvpnd/internal/store"
	"github.com/sentinel-official/sentinel-dvpnd/internal/worker"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/key"
	"github.com/sentinel-official/sentinel-go-sdk/v3/paymaster"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
)

const storeFileName = "data.db"

var serverRuntimes = map[string]runtimeFactory{} //nolint:gochecknoglobals

type runtimeFactory func(
	loaded *config.Loaded,
	db *gorm.DB,
	privateKey solana.PrivateKey,
	programClient *program.Client,
	sender *tx.Sender,
	log zerolog.Logger,
	reg prometheus.Registerer,
) (daemon.Runtime, error)

func checkWebSocket(ctx context.Context, loaded *config.Loaded, pin *rpcpin.Pin) error {
	if pin != nil {
		if _, active := pin.Pinned(); active {
			return nil
		}
	}

	conn, err := loaded.File.RPC.DialWS(ctx)
	if err != nil {
		return fmt.Errorf("connecting rpc: %w", err)
	}

	conn.Close()

	return nil
}

func closeStore(db *gorm.DB, log zerolog.Logger) {
	sqlDB, err := db.DB()
	if err != nil {
		log.Warn().Err(err).Msg("closing store")

		return
	}

	if err := sqlDB.Close(); err != nil {
		log.Warn().Err(err).Msg("closing store")
	}
}

func loadPrivateKey(loaded *config.Loaded) (solana.PrivateKey, error) {
	path, err := loaded.KeyPath()
	if err != nil {
		return nil, err
	}

	passphrase, err := loaded.LoadPassphrase()
	if err != nil {
		return nil, err
	}

	privateKey, err := key.Load(path, passphrase)
	if errors.Is(err, key.ErrKeyLocked) {
		return nil, fmt.Errorf("%w: set passphrase_file or %s", err, config.PassphraseEnv)
	}

	if err != nil {
		return nil, fmt.Errorf("loading key: %w", err)
	}

	return privateKey, nil
}

func newRPCClient(loaded *config.Loaded) (*rpcpin.Pin, *rpc.Client, error) {
	if loaded.Role != config.RoleClient {
		return nil, loaded.File.RPC.NewClient(), nil
	}

	pin, err := rpcpin.New(loaded.File.RPC.URL, loaded.File.RPC.Dialer())
	if err != nil {
		return nil, nil, err
	}

	return pin, loaded.File.RPC.NewClient(api.WithDialContext(pin.DialContext)), nil
}

func newRunCmd(opts *instanceOptions) *cobra.Command {
	cmd := &cobra.Command{
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDaemon(cmd.Context(), opts, cmd.Flags())
		},
		Short: "run the daemon in the foreground",
		Use:   "run",
	}
	config.AddFlags(cmd.Flags())

	return cmd
}

func newRuntime(
	loaded *config.Loaded,
	db *gorm.DB,
	privateKey solana.PrivateKey,
	programClient *program.Client,
	sender *tx.Sender,
	log zerolog.Logger,
	reg prometheus.Registerer,
	pin *rpcpin.Pin,
) (daemon.Runtime, error) {
	if loaded.Role == config.RoleClient {
		return client.New(loaded, db, privateKey, programClient, sender, log, reg, pin)
	}

	factory, ok := serverRuntimes[loaded.Role]
	if !ok {
		return nil, fmt.Errorf("%w: %s", config.ErrUnsupportedRole, loaded.Role)
	}

	return factory(loaded, db, privateKey, programClient, sender, log, reg)
}

func prepareRuntime(
	ctx context.Context,
	loaded *config.Loaded,
	rt daemon.Runtime,
	rpcClient *rpc.Client,
	pin *rpcpin.Pin,
	log zerolog.Logger,
) error {
	if recoverer, ok := rt.(daemon.Recoverer); ok {
		if err := recoverer.Recover(ctx); err != nil {
			if errors.Is(err, client.ErrDNSCleanupFailed) {
				return fmt.Errorf("recovering dns: %w", err)
			}

			if errors.Is(err, client.ErrServiceCleanupFailed) {
				return fmt.Errorf("recovering services: %w", err)
			}

			log.Warn().Err(err).Msg("recovery could not verify the persisted kill switch; StopSession releases it")
		}
	}

	if _, err := preflight.Run(ctx, loaded, log, rpcClient); err != nil {
		return err
	}

	return checkWebSocket(ctx, loaded, pin)
}

func runInstance(
	ctx context.Context,
	opts *instanceOptions,
	flags *pflag.FlagSet,
	out io.Writer,
	ready daemon.Ready,
) error {
	loaded, err := opts.load(flags)
	if err != nil {
		return err
	}

	release, err := daemon.Lock(loaded.Dir)
	if err != nil {
		return fmt.Errorf("locking instance folder: %w", err)
	}
	defer func() { _ = release() }()

	log := cli.NewLogger(out, loaded.File.Log).With().Str("role", loaded.Role).Logger()
	if err := warnCLIEnv(log); err != nil {
		return err
	}

	privateKey, err := loadPrivateKey(loaded)
	if err != nil {
		return err
	}

	return runRuntime(ctx, loaded, privateKey, log, ready)
}

func runRuntime(
	ctx context.Context,
	loaded *config.Loaded,
	privateKey solana.PrivateKey,
	log zerolog.Logger,
	ready daemon.Ready,
) error {
	pin, rpcClient, err := newRPCClient(loaded)
	if err != nil {
		return err
	}

	programClient, err := program.NewClientFromBuild(rpcClient, loaded.File.RPC.Commitment)
	if err != nil {
		return err
	}

	sender := tx.NewSender(
		rpcClient,
		rpcpin.NewWSDialer(loaded.File.RPC.WSURL, loaded.File.RPC.Timeout, pin, log),
		loaded.File.Tx,
		log,
	)
	sender.ProgramIDs = tx.ProgramIDs{DVPN: programClient.ProgramID}

	if cfg := loaded.File.Tx.Paymaster; cfg.URL != "" {
		kora, err := paymaster.NewKora(rpcClient, cfg, programClient.Mint)
		if err != nil {
			return fmt.Errorf("building paymaster: %w", err)
		}

		sender.Paymaster = kora
	}

	db, err := store.Open(filepath.Join(loaded.Dir, storeFileName))
	if err != nil {
		return fmt.Errorf("opening store: %w", err)
	}
	defer closeStore(db, log)

	reg := daemon.NewRegistry()
	roleReg := prometheus.WrapRegistererWith(prometheus.Labels{"role": loaded.Role}, reg)

	rt, err := newRuntime(loaded, db, privateKey, programClient, sender, log, roleReg, pin)
	if err != nil {
		return fmt.Errorf("building %s runtime: %w", loaded.Role, err)
	}

	if err := store.Migrate(ctx, db, rt.Models()...); err != nil {
		return fmt.Errorf("migrating store: %w", err)
	}

	if err := prepareRuntime(ctx, loaded, rt, rpcClient, pin, log); err != nil {
		return err
	}

	if loaded.Role == config.RoleClient {
		release, err := startTunnelGuard(loaded)
		if err != nil {
			log.Warn().Err(err).Msg("tunnel guard not started; a killed daemon leaves its tunnel up until the next start")
		} else {
			// Released only after serve has returned, i.e. after the daemon's own teardown.
			defer func() {
				if err := release(); err != nil {
					log.Warn().Err(err).Msg("releasing the tunnel guard")
				}
			}()
		}
	}

	return serve(ctx, loaded, rt, reg, roleReg, log, ready)
}

func serve(
	ctx context.Context,
	loaded *config.Loaded,
	rt daemon.Runtime,
	reg *prometheus.Registry,
	roleReg prometheus.Registerer,
	log zerolog.Logger,
	ready daemon.Ready,
) error {
	listener, err := daemon.Listen(ctx, loaded.Endpoint(), config.AccessGroup(loaded.Tag, loaded.Dir))
	if err != nil {
		return fmt.Errorf("listening: %w", err)
	}
	defer func() { _ = listener.Close() }()

	server := daemon.NewServer(log, reg)

	var aux []func(context.Context) error

	if loaded.File.Metrics.Enabled {
		var listenConfig net.ListenConfig

		metricsListener, err := listenConfig.Listen(ctx, "tcp", loaded.File.Metrics.Listen)
		if err != nil {
			return fmt.Errorf("listening on metrics: %w", err)
		}

		defer func() { _ = metricsListener.Close() }()

		aux = append(aux, func(ctx context.Context) error {
			return daemon.ServeMetrics(ctx, metricsListener, reg)
		})
	}

	runner, err := worker.New(log, roleReg)
	if err != nil {
		return err
	}

	return daemon.Run(ctx, rt, server, listener, runner, log, ready, aux...)
}

func warnCLIEnv(log zerolog.Logger) error {
	mismatches, err := config.DetectCLIEnv(os.Environ())
	if err != nil {
		return fmt.Errorf("detecting cli variables: %w", err)
	}

	for _, m := range mismatches {
		log.Warn().Str("expected", m.Expected).Str("variable", m.Actual).Msg("ignoring a sentinel-dvpn cli variable")
	}

	return nil
}
