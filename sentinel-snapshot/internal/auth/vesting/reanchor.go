package vesting

import (
	"cosmossdk.io/math"
)

type Original struct {
	Kind       Kind
	StartUnix  int64
	EndUnix    int64
	Periods    []Period
	TotalUDVPN uint64
}

func (o Original) ToSchedule() Schedule {
	return Schedule(o)
}

func Reanchor(original Original, locked uint64, haltUnix int64) (Schedule, bool) {
	if locked == 0 {
		return Schedule{}, false
	}

	if original.TotalUDVPN == 0 {
		return Schedule{}, false
	}

	vested := original.ToSchedule().UnlockedAt(haltUnix)
	unvested := original.TotalUDVPN - vested

	switch original.Kind {
	case KindDelayed:
		if original.EndUnix <= haltUnix {
			return Schedule{}, false
		}

		return Schedule{
			Kind:       KindDelayed,
			StartUnix:  haltUnix,
			EndUnix:    original.EndUnix,
			TotalUDVPN: locked,
		}, true

	case KindContinuous:
		if original.EndUnix <= haltUnix {
			return Schedule{}, false
		}

		start := max(haltUnix, original.StartUnix)

		var end int64
		if locked == unvested {
			end = original.EndUnix
		} else {
			window := original.EndUnix - original.StartUnix
			duration := math.LegacyNewDecFromInt(math.NewIntFromUint64(locked)).
				MulInt64(window).QuoInt(math.NewIntFromUint64(original.TotalUDVPN)).Ceil().TruncateInt64()
			end = start + duration
		}

		return Schedule{
			Kind:       KindContinuous,
			StartUnix:  start,
			EndUnix:    end,
			TotalUDVPN: locked,
		}, true

	case KindPeriodic:
		return reanchorPeriodic(original.StartUnix, original.Periods, locked, haltUnix)
	}

	return Schedule{}, false
}

func reanchorPeriodic(originalStartUnix int64, periods []Period, locked uint64, haltUnix int64) (Schedule, bool) {
	periodEndUnix := originalStartUnix

	type remainingPeriod struct {
		lengthSeconds int64
		amount        uint64
	}

	var remaining []remainingPeriod

	for _, period := range periods {
		periodEndUnix += period.LengthSeconds
		if periodEndUnix <= haltUnix {
			continue
		}

		lengthSeconds := period.LengthSeconds
		if len(remaining) == 0 {
			lengthSeconds = periodEndUnix - haltUnix
		}

		remaining = append(remaining, remainingPeriod{
			lengthSeconds: lengthSeconds,
			amount:        period.AmountUDVPN,
		})
	}

	if len(remaining) == 0 {
		return Schedule{}, false
	}

	reanchored := make([]Period, 0, len(remaining))

	var allocated uint64

	for _, period := range remaining {
		amount := period.amount
		if allocated+amount >= locked {
			amount = locked - allocated
			reanchored = append(reanchored, Period{
				LengthSeconds: period.lengthSeconds,
				AmountUDVPN:   amount,
			})

			break
		}

		reanchored = append(reanchored, Period{
			LengthSeconds: period.lengthSeconds,
			AmountUDVPN:   amount,
		})
		allocated += amount
	}

	end := haltUnix
	for _, period := range reanchored {
		end += period.LengthSeconds
	}

	return Schedule{
		Kind:       KindPeriodic,
		StartUnix:  haltUnix,
		EndUnix:    end,
		Periods:    reanchored,
		TotalUDVPN: locked,
	}, true
}
