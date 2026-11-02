package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

//nolint:gocritic
func main() {
	logLevel := &slog.LevelVar{}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], logLevel); err != nil {
		slog.Error("snapshot failed", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, logLevel *slog.LevelVar) error {
	if len(args) == 0 {
		return errors.New("usage: snapshot <snapshot|verify|prune> [flags]")
	}

	switch args[0] {
	case "snapshot":
		a, err := parseSnapshotArgs(args[1:])
		if err != nil {
			return err
		}

		if err := applyLogLevel(logLevel, a.LogLevel); err != nil {
			return err
		}

		return runSnapshot(ctx, a)
	case "verify":
		a, err := parseVerifyArgs(args[1:])
		if err != nil {
			return err
		}

		if err := applyLogLevel(logLevel, a.LogLevel); err != nil {
			return err
		}

		return runVerify(ctx, a)
	case "prune":
		a, err := parsePruneArgs(args[1:])
		if err != nil {
			return err
		}

		if err := applyLogLevel(logLevel, a.LogLevel); err != nil {
			return err
		}

		return runPrune(ctx, a)
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}
