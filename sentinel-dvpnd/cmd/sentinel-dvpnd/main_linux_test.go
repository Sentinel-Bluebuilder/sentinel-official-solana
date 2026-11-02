package main_test

import (
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"

	main "github.com/sentinel-official/sentinel-dvpnd/cmd/sentinel-dvpnd"
	"github.com/sentinel-official/sentinel-dvpnd/internal/client"
	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-dvpnd/internal/daemon"
	"github.com/sentinel-official/sentinel-dvpnd/internal/node"
	"github.com/sentinel-official/sentinel-dvpnd/internal/provider"
	"github.com/sentinel-official/sentinel-dvpnd/internal/rpcpin"
	"github.com/sentinel-official/sentinel-dvpnd/internal/store"
	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	"github.com/sentinel-official/sentinel-dvpnd/internal/worker"
	"github.com/sentinel-official/sentinel-go-sdk/v3/key"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
)

func TestNewRuntime_EveryRoleOnOneRegistry(t *testing.T) {
	t.Parallel()

	cases := []struct {
		role string
		want any
	}{
		{config.RoleClient, &client.Runtime{}},
		{config.RoleNode, &node.Runtime{}},
		{config.RoleProvider, &provider.Runtime{}},
	}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			t.Parallel()

			fakeRPC := sdktest.NewFakeRPC(t)
			home := t.TempDir()
			tag := testutil.NewInstanceTag(t, tc.role)

			args := []string{"--instance", string(tag), "--owner", randomOwner(t), "--plain-key"}

			switch tc.role {
			case config.RoleNode:
				args = append(args, "--country", "DE", "--api-hosts", "203.0.113.1", "--service", "wireguard")
			case config.RoleProvider:
				args = append(args, "--api-hosts", "203.0.113.1")
			}

			_, err := runInitCmd(t, home, args...)
			require.NoError(t, err)

			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			config.AddFlags(flags)
			require.NoError(t, flags.Set("rpc.url", fakeRPC.Config().URL))
			require.NoError(t, flags.Set("rpc.ws-url", fakeRPC.Config().WSURL))
			loaded, err := config.Load(tag, home, flags)
			require.NoError(t, err)

			loaded.Root = t.TempDir()
			keyPath, err := loaded.KeyPath()
			require.NoError(t, err)
			privateKey, err := key.Load(keyPath, "")
			require.NoError(t, err)
			db, err := store.Open(filepath.Join(loaded.Dir, "data.db"))
			require.NoError(t, err)
			t.Cleanup(func() {
				sqlDB, dbErr := db.DB()
				if dbErr == nil {
					_ = sqlDB.Close()
				}
			})

			programID := solana.MustPublicKeyFromBase58("SentineLDVPN1111111111111111111111111111111")
			programClient := program.NewClient(fakeRPC.NewClient(), programID, solana.PublicKey{2}, rpc.CommitmentConfirmed)
			log := zerolog.Nop()
			sender := tx.NewSender(fakeRPC.NewClient(), nil, tx.DefaultConfig(), log)
			pin, err := rpcpin.New(fakeRPC.Config().URL, fakeRPC.Config().Dialer())
			require.NoError(t, err)

			reg := daemon.NewRegistry()
			roleReg := prometheus.WrapRegistererWith(prometheus.Labels{"role": tc.role}, reg)
			_ = daemon.NewServer(log, reg)
			_, err = worker.New(log, roleReg)
			require.NoError(t, err)
			rt, err := main.NewRuntime(loaded, db, privateKey, programClient, sender, log, roleReg, pin)
			require.NoError(t, err)
			require.IsType(t, tc.want, rt)
		})
	}
}
