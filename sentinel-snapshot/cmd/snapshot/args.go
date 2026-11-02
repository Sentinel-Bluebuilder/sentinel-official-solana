package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"runtime"

	cmttypes "github.com/cometbft/cometbft/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/anchor"
)

type haltArgs struct {
	HeaderPath       string
	CommitPath       string
	ValidatorSetPath string
	ChainID          string
}

func (a *haltArgs) authenticate() (*cmttypes.Header, error) {
	header, err := anchor.LoadHeader(a.HeaderPath)
	if err != nil {
		return nil, err
	}

	commit, err := anchor.LoadCommit(a.CommitPath)
	if err != nil {
		return nil, err
	}

	validators, err := anchor.LoadValidatorSet(a.ValidatorSetPath)
	if err != nil {
		return nil, err
	}

	if err := anchor.VerifyCommit(a.ChainID, validators, header, commit); err != nil {
		return nil, fmt.Errorf("header H is not authenticated: %w", err)
	}

	return header, nil
}

func (a *haltArgs) register(fs *flag.FlagSet) {
	fs.StringVar(&a.HeaderPath, "header", "", "path to header H JSON (cmtjson)")
	fs.StringVar(&a.CommitPath, "commit", "", "path to a valid commit for header H")
	fs.StringVar(&a.ValidatorSetPath, "validator-set", "", "path to the published trust-anchor validator set")
	fs.StringVar(&a.ChainID, "chain-id", "", "chain id the commit was signed over")
}

func (a *haltArgs) validate() error {
	if a.HeaderPath == "" || a.CommitPath == "" || a.ValidatorSetPath == "" || a.ChainID == "" {
		return errors.New("requires the full trust anchor: --header, --commit, --validator-set, --chain-id")
	}

	return nil
}

type sharedArgs struct {
	Workers  int
	LogLevel string
}

func (a *sharedArgs) register(fs *flag.FlagSet) {
	fs.IntVar(&a.Workers, "workers", runtime.NumCPU(), "parallel worker count (1 = sequential)")
	fs.StringVar(&a.LogLevel, "log-level", "info", "log level: debug|info|warn|error")
}

func (a *sharedArgs) validate() error {
	if a.Workers < 1 {
		return fmt.Errorf("--workers must be >= 1, got %d", a.Workers)
	}

	return nil
}

type pruneArgs struct {
	haltArgs
	sharedArgs

	AppDBPath string
	Height    int64
}

func parsePruneArgs(args []string) (pruneArgs, error) {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)

	var a pruneArgs
	a.haltArgs.register(fs)
	a.sharedArgs.register(fs)
	fs.StringVar(&a.AppDBPath, "app-db", "", "path to application.db")
	fs.Int64Var(&a.Height, "height", 0, "halt height H")

	if err := fs.Parse(args); err != nil {
		return a, fmt.Errorf("parse prune flags: %w", err)
	}

	if err := a.haltArgs.validate(); err != nil {
		return a, fmt.Errorf("prune %w", err)
	}

	if err := a.sharedArgs.validate(); err != nil {
		return a, fmt.Errorf("prune %w", err)
	}

	if a.AppDBPath == "" || a.Height <= 0 {
		return a, errors.New("prune requires --app-db and --height")
	}

	return a, nil
}

type snapshotArgs struct {
	haltArgs
	sharedArgs

	AppDBPath          string
	LedgerPath         string
	ReconciliationPath string
}

func parseSnapshotArgs(args []string) (snapshotArgs, error) {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)

	var a snapshotArgs
	a.haltArgs.register(fs)
	a.sharedArgs.register(fs)
	fs.StringVar(&a.AppDBPath, "app-db", "", "path to application.db (pruned to H−1)")
	fs.StringVar(&a.LedgerPath, "out", "", "output ledger path")
	fs.StringVar(&a.ReconciliationPath, "reconciliation", "", "output reconciliation.json (default: beside --out)")

	if err := fs.Parse(args); err != nil {
		return a, fmt.Errorf("parse snapshot flags: %w", err)
	}

	if err := a.haltArgs.validate(); err != nil {
		return a, fmt.Errorf("snapshot %w", err)
	}

	if err := a.sharedArgs.validate(); err != nil {
		return a, fmt.Errorf("snapshot %w", err)
	}

	if a.AppDBPath == "" || a.LedgerPath == "" {
		return a, errors.New("snapshot requires --app-db and --out")
	}

	return a, nil
}

type verifyArgs struct {
	haltArgs
	sharedArgs

	AppDBPath string
}

func parseVerifyArgs(args []string) (verifyArgs, error) {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)

	var a verifyArgs
	a.haltArgs.register(fs)
	a.sharedArgs.register(fs)
	fs.StringVar(&a.AppDBPath, "app-db", "", "path to application.db")

	if err := fs.Parse(args); err != nil {
		return a, fmt.Errorf("parse verify flags: %w", err)
	}

	if err := a.haltArgs.validate(); err != nil {
		return a, fmt.Errorf("verify %w", err)
	}

	if err := a.sharedArgs.validate(); err != nil {
		return a, fmt.Errorf("verify %w", err)
	}

	if a.AppDBPath == "" {
		return a, errors.New("verify requires --app-db")
	}

	return a, nil
}

func applyLogLevel(level *slog.LevelVar, name string) error {
	switch name {
	case "debug":
		level.Set(slog.LevelDebug)
	case "info":
		level.Set(slog.LevelInfo)
	case "warn":
		level.Set(slog.LevelWarn)
	case "error":
		level.Set(slog.LevelError)
	default:
		return fmt.Errorf("invalid --log-level %q", name)
	}

	return nil
}
