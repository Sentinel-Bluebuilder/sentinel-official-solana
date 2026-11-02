package xray

import (
	"github.com/sentinel-official/sentinel-go-sdk/v3/internal/v2raycore"
)

type API = api

func ServerConfigToCore(c ServerConfig) v2raycore.ServerConfig[Extensions] { return c.toCore() }
