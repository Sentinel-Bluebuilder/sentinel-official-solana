//go:build integration

package ibcescrow

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	ibctransfertypes "github.com/cosmos/ibc-go/v7/modules/apps/transfer/types"

	"github.com/sentinel-official/sentinel-snapshot/internal/app"
)

type Channel struct {
	PortID    string
	ChannelID string
	Address   sdk.AccAddress
}

func Channels(h *app.Handle) ([]Channel, error) {
	channels := h.App().IBCKeeper.ChannelKeeper.GetAllChannels(h.Context())

	escrows := make([]Channel, 0, len(channels))
	for _, channel := range channels {
		if channel.PortId != ibctransfertypes.PortID {
			continue
		}

		address := ibctransfertypes.GetEscrowAddress(channel.PortId, channel.ChannelId)
		escrows = append(escrows, Channel{
			PortID:    channel.PortId,
			ChannelID: channel.ChannelId,
			Address:   address,
		})
	}

	return validateNoDuplicateAddresses(escrows)
}

func validateNoDuplicateAddresses(channels []Channel) ([]Channel, error) {
	seen := make(map[string]bool, len(channels))
	for _, channel := range channels {
		key := channel.Address.String()
		if seen[key] {
			return nil, fmt.Errorf("ibcescrow: duplicate escrow address %s for %s/%s", key, channel.PortID, channel.ChannelID)
		}

		seen[key] = true
	}

	return channels, nil
}
