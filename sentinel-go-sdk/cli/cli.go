package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sentinel-official/sentinel-go-sdk/v3/version"
)

const (
	LogFormatConsole = "console"
	LogFormatJSON    = "json"
)

type LogConfig struct {
	Format string `json:"format" toml:"format" validate:"oneof=json console"`
	Level  string `json:"level"  toml:"level"  validate:"oneof=trace debug info warn error"`
}

func DefaultHome(name string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return name
	}

	return filepath.Join(home, name)
}

func NewLogger(writer io.Writer, cfg LogConfig) zerolog.Logger {
	level, err := zerolog.ParseLevel(cfg.Level)
	if err != nil || cfg.Level == "" {
		level = zerolog.InfoLevel
	}

	if cfg.Format == LogFormatConsole {
		writer := zerolog.ConsoleWriter{Out: writer}

		return zerolog.New(writer).Level(level).With().Timestamp().Logger()
	}

	return zerolog.New(writer).Level(level).With().Timestamp().Logger()
}

func NewVersionCmd() *cobra.Command {
	run := func(cmd *cobra.Command, _ []string) error {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(version.Current())
	}

	cmd := &cobra.Command{
		Args:  cobra.NoArgs,
		RunE:  run,
		Short: "print build identity",
		Use:   "version",
	}

	return cmd
}

func Run(root *cobra.Command) int {
	signals := make(chan os.Signal, 1)

	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan os.Signal, 1)

	go func() {
		select {
		case sig := <-signals:
			received <- sig

			cancel()
		case <-ctx.Done():
		}
	}()

	root.SilenceUsage = true
	root.SilenceErrors = true

	if err := root.ExecuteContext(ctx); err != nil {
		if ctx.Err() != nil {
			if !canceledOnly(err) {
				_, _ = fmt.Fprintln(root.ErrOrStderr(), "error:", err)
			}

			return exitCodeForSignal(<-received)
		}

		if errors.Is(err, context.Canceled) {
			return 130
		}

		_, _ = fmt.Fprintln(root.ErrOrStderr(), "error:", err)

		return 1
	}

	return 0
}

func canceledOnly(err error) bool {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if !canceledOnly(child) {
				return false
			}
		}

		return true
	}

	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return status.Code(err) == codes.Canceled && canceledOnly(wrapped.Unwrap())
	}

	return errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled
}

func exitCodeForSignal(sig os.Signal) int {
	switch sig {
	case syscall.SIGINT:
		return 130
	case syscall.SIGTERM:
		return 143
	default:
		return 1
	}
}
