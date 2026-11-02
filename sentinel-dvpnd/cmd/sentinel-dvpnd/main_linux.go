package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"gorm.io/gorm"

	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
	"github.com/sentinel-official/sentinel-dvpnd/internal/daemon"
	"github.com/sentinel-official/sentinel-dvpnd/internal/node"
	"github.com/sentinel-official/sentinel-dvpnd/internal/provider"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/tx"
)

//nolint:gochecknoinits
func init() {
	serverRuntimes[config.RoleNode] = newNodeRuntime
	serverRuntimes[config.RoleProvider] = newProviderRuntime
}

func newNodeRuntime(
	loaded *config.Loaded,
	db *gorm.DB,
	privateKey solana.PrivateKey,
	programClient *program.Client,
	sender *tx.Sender,
	log zerolog.Logger,
	reg prometheus.Registerer,
) (daemon.Runtime, error) {
	return node.New(loaded, db, privateKey, programClient, sender, log, reg)
}

func newProviderRuntime(
	loaded *config.Loaded,
	db *gorm.DB,
	privateKey solana.PrivateKey,
	programClient *program.Client,
	sender *tx.Sender,
	log zerolog.Logger,
	reg prometheus.Registerer,
) (daemon.Runtime, error) {
	return provider.New(loaded, db, privateKey, programClient, sender, log, reg)
}
