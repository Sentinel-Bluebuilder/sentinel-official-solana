package distribution

import (
	"bytes"
	"sort"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type Credit struct {
	Address sdk.AccAddress
	Amount  math.LegacyDec
}

func ClampToOutstanding(
	outstanding, commission math.LegacyDec, credits []Credit,
) (credited map[string]math.Int, commissionCredited math.Int) {
	if commission.GT(outstanding) {
		commission = outstanding
	}

	running := outstanding.Sub(commission)

	ordered := append([]Credit(nil), credits...)
	sort.Slice(ordered, func(i, j int) bool {
		return bytes.Compare(ordered[i].Address, ordered[j].Address) < 0
	})

	credited = make(map[string]math.Int, len(ordered))
	for _, c := range ordered {
		clamped := c.Amount
		if clamped.GT(running) {
			clamped = running
		}

		running = running.Sub(clamped)
		credited[c.Address.String()] = clamped.TruncateInt()
	}

	return credited, commission.TruncateInt()
}
