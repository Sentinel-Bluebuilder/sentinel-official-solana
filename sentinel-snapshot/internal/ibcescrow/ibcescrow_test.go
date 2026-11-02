//go:build integration

package ibcescrow_test

import (
	"strings"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	ibctransfertypes "github.com/cosmos/ibc-go/v7/modules/apps/transfer/types"
	channeltypes "github.com/cosmos/ibc-go/v7/modules/core/04-channel/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/apptest"
	"github.com/sentinel-official/sentinel-snapshot/internal/ibcescrow"
	sentinelapp "github.com/sentinel-official/sentinelhub/v12/app"
)

func TestValidateNoDuplicateAddresses_Duplicate(t *testing.T) {
	t.Parallel()

	address := sdk.AccAddress([]byte("duplicate-escrow-address"))
	channels := []ibcescrow.Channel{
		{PortID: "transfer", ChannelID: "channel-0", Address: address},
		{PortID: "transfer", ChannelID: "channel-1", Address: address},
	}

	_, err := ibcescrow.ValidateNoDuplicateAddresses(channels)
	if err == nil {
		t.Fatal("expected duplicate escrow address to fail validation")
	}

	if !strings.Contains(err.Error(), address.String()) {
		t.Fatalf("error must name the duplicate address: %v", err)
	}
}

//nolint:paralleltest
func TestChannels_TransferOnlyAllStates(t *testing.T) {
	h := apptest.SeedHandle(t, func(ctx sdk.Context, application *sentinelapp.App) {
		seedChannel(ctx, application, ibctransfertypes.PortID, "channel-0", channeltypes.OPEN)
		seedChannel(ctx, application, ibctransfertypes.PortID, "channel-9", channeltypes.CLOSED)
		seedChannel(ctx, application, "icahost", "channel-1", channeltypes.OPEN)
	})

	got, err := ibcescrow.Channels(h)
	if err != nil {
		t.Fatalf("Channels: %v", err)
	}

	want := map[string]string{
		ibctransfertypes.GetEscrowAddress(ibctransfertypes.PortID, "channel-0").String(): "channel-0",
		ibctransfertypes.GetEscrowAddress(ibctransfertypes.PortID, "channel-9").String(): "channel-9",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d escrow addresses, want %d: %+v", len(got), len(want), got)
	}

	for _, channel := range got {
		if channel.PortID != ibctransfertypes.PortID {
			t.Fatalf("non-transfer port leaked: %+v", channel)
		}

		if want[channel.Address.String()] != channel.ChannelID {
			t.Fatalf("unexpected escrow entry: %+v", channel)
		}
	}
}
