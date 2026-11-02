package main

import (
	"os"

	"github.com/sentinel-official/sentinel-dvpn/internal/cli"
	_ "github.com/sentinel-official/sentinel-go-sdk/v3/amneziawg"
	sdkcli "github.com/sentinel-official/sentinel-go-sdk/v3/cli"
	_ "github.com/sentinel-official/sentinel-go-sdk/v3/hysteria2"
	_ "github.com/sentinel-official/sentinel-go-sdk/v3/openvpn"
	_ "github.com/sentinel-official/sentinel-go-sdk/v3/v2ray"
	_ "github.com/sentinel-official/sentinel-go-sdk/v3/wireguard"
	_ "github.com/sentinel-official/sentinel-go-sdk/v3/xray"
)

func main() {
	os.Exit(sdkcli.Run(cli.NewRootCmd()))
}
