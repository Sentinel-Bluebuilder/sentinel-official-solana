package config_test

import (
	"testing"

	"github.com/sentinel-official/sentinel-go-sdk/v3/cli"
	"github.com/sentinel-official/sentinel-go-sdk/v3/version"
)

func TestMain(m *testing.M) { version.Network = cli.NetworkLocalnet; m.Run() }
