package config_test

import (
	"testing"

	"github.com/spf13/pflag"

	"github.com/sentinel-official/sentinel-dvpnd/internal/config"
)

func newFlags(t *testing.T) *pflag.FlagSet {
	t.Helper()

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	config.AddFlags(flags)

	return flags
}
