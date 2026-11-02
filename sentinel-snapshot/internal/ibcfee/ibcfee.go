//go:build integration

package ibcfee

import (
	ibcfeetypes "github.com/cosmos/ibc-go/v7/modules/apps/29-fee/types"

	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
)

func Enabled() bool {
	_, ok := sentinelapp.ModuleBasics[ibcfeetypes.ModuleName]

	return ok
}
