//go:build integration

package ibcescrow_test

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	channeltypes "github.com/cosmos/ibc-go/v7/modules/core/04-channel/types"

	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
)

func seedChannel(ctx sdk.Context, application *sentinelapp.App, port, channel string, state channeltypes.State) {
	application.IBCKeeper.ChannelKeeper.SetChannel(ctx, port, channel, channeltypes.Channel{
		State:          state,
		Ordering:       channeltypes.UNORDERED,
		Counterparty:   channeltypes.NewCounterparty(port, channel),
		ConnectionHops: []string{"connection-0"},
		Version:        "ics20-1",
	})
}
