package amneziawg

import (
	"github.com/sentinel-official/sentinel-go-sdk/v3/internal/wgcore"
	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

func PreflightClient(goos string) []service.PreflightCheck { return preflightClient(goos) }

func Variant() wgcore.Variant { return variant() }
