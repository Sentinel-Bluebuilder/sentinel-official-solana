package vesting

import (
	"encoding/binary"
	"errors"
	"fmt"
	stdmath "math"

	"cosmossdk.io/math"
)

const (
	KindContinuous Kind = 0
	KindDelayed    Kind = 1
	KindPeriodic   Kind = 2
)

const (
	MaxJSSafeInteger int64 = 1<<53 - 1
	MaxPeriods             = 512
)

type Kind uint8

func KindFromTypeURL(typeURL string) (kind Kind, permanent bool, err error) {
	switch typeURL {
	case "/cosmos.vesting.v1beta1.ContinuousVestingAccount":
		return KindContinuous, false, nil
	case "/cosmos.vesting.v1beta1.DelayedVestingAccount":
		return KindDelayed, false, nil
	case "/cosmos.vesting.v1beta1.PeriodicVestingAccount":
		return KindPeriodic, false, nil
	case "/cosmos.vesting.v1beta1.PermanentLockedAccount":
		return 0, true, nil
	default:
		return 0, false, fmt.Errorf("unsupported vesting @type %q", typeURL)
	}
}

type Period struct {
	LengthSeconds int64
	AmountUDVPN   uint64
}

type Schedule struct {
	Kind       Kind
	StartUnix  int64
	EndUnix    int64
	Periods    []Period
	TotalUDVPN uint64
}

func (s Schedule) EncodePreimage() ([]byte, error) {
	periodCount, err := boundedUint32Len(len(s.Periods), "periods")
	if err != nil {
		return nil, err
	}

	buf := make([]byte, 0, 1+8+8+4+len(s.Periods)*16+8)
	buf = append(buf, byte(s.Kind))
	buf = appendI64LE(buf, s.StartUnix)
	buf = appendI64LE(buf, s.EndUnix)

	buf = appendU32LE(buf, periodCount)
	for _, p := range s.Periods {
		buf = appendI64LE(buf, p.LengthSeconds)
		buf = appendU64LE(buf, p.AmountUDVPN)
	}

	buf = appendU64LE(buf, s.TotalUDVPN)

	return buf, nil
}

func (s Schedule) UnlockedAt(atUnix int64) uint64 {
	switch s.Kind {
	case KindDelayed:
		if atUnix >= s.EndUnix {
			return s.TotalUDVPN
		}

		return 0
	case KindContinuous:
		if atUnix <= s.StartUnix {
			return 0
		}

		if atUnix >= s.EndUnix {
			return s.TotalUDVPN
		}

		elapsed := atUnix - s.StartUnix
		duration := s.EndUnix - s.StartUnix
		scalar := math.LegacyNewDec(elapsed).Quo(math.LegacyNewDec(duration))
		vested := math.LegacyNewDecFromInt(math.NewIntFromUint64(s.TotalUDVPN)).Mul(scalar).RoundInt()

		return vested.Uint64()
	case KindPeriodic:
		if atUnix <= s.StartUnix {
			return 0
		}

		if atUnix >= s.EndUnix {
			return s.TotalUDVPN
		}

		elapsed := atUnix - s.StartUnix

		var (
			elapsedPeriods int64
			vested         uint64
		)

		for _, period := range s.Periods {
			elapsedPeriods += period.LengthSeconds
			if elapsedPeriods <= elapsed {
				vested += period.AmountUDVPN
			} else {
				break
			}
		}

		return vested
	default:
		return 0
	}
}

func (s Schedule) Validate(locked uint64) error {
	if s.EndUnix <= s.StartUnix {
		return fmt.Errorf("end %d not after start %d", s.EndUnix, s.StartUnix)
	}

	if s.EndUnix > MaxJSSafeInteger {
		return fmt.Errorf("end %d exceeds the JS-safe bound %d", s.EndUnix, MaxJSSafeInteger)
	}

	if s.StartUnix < 0 {
		return fmt.Errorf("start %d is negative (a re-anchored schedule starts at the halt time)", s.StartUnix)
	}

	if s.TotalUDVPN == 0 {
		return errors.New("total is zero")
	}

	if s.TotalUDVPN != locked {
		return fmt.Errorf("schedule total %d != locked %d", s.TotalUDVPN, locked)
	}

	switch s.Kind {
	case KindContinuous, KindDelayed:
		if len(s.Periods) != 0 {
			return fmt.Errorf("continuous/delayed schedule must have no periods, got %d", len(s.Periods))
		}
	case KindPeriodic:
		if len(s.Periods) == 0 || len(s.Periods) > MaxPeriods {
			return fmt.Errorf("periodic period count %d out of [1,%d]", len(s.Periods), MaxPeriods)
		}

		var (
			sumAmount        uint64
			sumLengthSeconds int64
		)

		for _, p := range s.Periods {
			if p.LengthSeconds <= 0 {
				return fmt.Errorf("non-positive period length %d", p.LengthSeconds)
			}

			if p.LengthSeconds > MaxJSSafeInteger {
				return fmt.Errorf("period length %d exceeds the JS-safe bound %d", p.LengthSeconds, MaxJSSafeInteger)
			}

			if sumAmount > stdmath.MaxUint64-p.AmountUDVPN {
				return errors.New("sum of period amounts (Σ) overflows uint64")
			}

			sumAmount += p.AmountUDVPN
			if sumLengthSeconds > stdmath.MaxInt64-p.LengthSeconds {
				return errors.New("sum of period lengths (Σ) overflows int64")
			}

			sumLengthSeconds += p.LengthSeconds
		}

		if sumAmount != s.TotalUDVPN {
			return fmt.Errorf("sum of period amounts (Σ) %d != total %d", sumAmount, s.TotalUDVPN)
		}

		if s.StartUnix > stdmath.MaxInt64-sumLengthSeconds {
			return fmt.Errorf("start %d + Σlen %d overflows int64", s.StartUnix, sumLengthSeconds)
		}

		if s.StartUnix+sumLengthSeconds != s.EndUnix {
			return fmt.Errorf("end %d != start + Σlen %d", s.EndUnix, s.StartUnix+sumLengthSeconds)
		}
	default:
		return fmt.Errorf("unknown kind %d", s.Kind)
	}

	return nil
}

func appendI64LE(b []byte, v int64) []byte {
	var t [8]byte
	binary.LittleEndian.PutUint64(t[:], uint64(v))

	return append(b, t[:]...)
}

func appendU32LE(b []byte, v uint32) []byte {
	var t [4]byte
	binary.LittleEndian.PutUint32(t[:], v)

	return append(b, t[:]...)
}

func appendU64LE(b []byte, v uint64) []byte {
	var t [8]byte
	binary.LittleEndian.PutUint64(t[:], v)

	return append(b, t[:]...)
}

func boundedUint32Len(n int, what string) (uint32, error) {
	if uint64(n) > stdmath.MaxUint32 {
		return 0, fmt.Errorf("%s length %d exceeds uint32 wire-format bound", what, n)
	}

	return uint32(n), nil
}
