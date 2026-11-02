package session

import (
	"errors"
	"fmt"
	"math"
	"math/bits"

	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
)

const (
	BasisPerGB   Basis  = 1
	BasisPerHour Basis  = 2
	BytesPerGB   uint64 = 1_000_000_000
)

var ErrInvalidBasis = errors.New("invalid basis")

//nolint:gochecknoglobals
var basisNames = map[Basis]string{
	BasisPerGB:   "per_gb",
	BasisPerHour: "per_hour",
}

type Basis uint8

func (b Basis) MarshalText() ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}

	return []byte(b.String()), nil
}

func (b Basis) String() string {
	if name, ok := basisNames[b]; ok {
		return name
	}

	return "unknown"
}

func (b Basis) Unit() uint64 {
	if b == BasisPerHour {
		return program.SecondsPerHour
	}

	return BytesPerGB
}

func (b *Basis) UnmarshalText(text []byte) error {
	parsed, err := ParseBasis(string(text))
	if err != nil {
		return err
	}

	*b = parsed

	return nil
}

func (b Basis) Validate() error {
	if _, ok := basisNames[b]; !ok {
		return fmt.Errorf("%w: %d", ErrInvalidBasis, b)
	}

	return nil
}

type Terms struct {
	Basis Basis  `json:"basis"`
	Price uint64 `json:"price"`
}

func (t Terms) Validate() error {
	return t.Basis.Validate()
}

type Usage struct {
	RxBytes uint64 `json:"rx_bytes"`
	Seconds uint64 `json:"seconds"`
	TxBytes uint64 `json:"tx_bytes"`
}

func (u Usage) Amount(basis Basis) uint64 {
	if basis == BasisPerHour {
		return u.Seconds
	}

	sum, carry := bits.Add64(u.RxBytes, u.TxBytes, 0)
	if carry != 0 {
		return math.MaxUint64
	}

	return sum
}

func Headroom(paid, owed uint64) uint64 {
	return paid - min(paid, owed)
}

func Owed(basis Basis, price, amount uint64) uint64 {
	unit := basis.Unit()

	high, low := bits.Mul64(amount, price)
	if high >= unit {
		return math.MaxUint64
	}

	quotient, remainder := bits.Div64(high, low, unit)
	if remainder != 0 {
		if quotient == math.MaxUint64 {
			return math.MaxUint64
		}

		quotient++
	}

	return quotient
}

func ParseBasis(text string) (Basis, error) {
	for b, name := range basisNames {
		if name == text {
			return b, nil
		}
	}

	return 0, fmt.Errorf("%w: %q", ErrInvalidBasis, text)
}

func Tolerance(local, block uint64, percent uint8) uint64 {
	const divisor = 100

	sum, carry := bits.Add64(local, block, 0)
	high, low := bits.Mul64(sum, uint64(percent))
	high += carry * uint64(percent)

	if high >= divisor {
		return math.MaxUint64
	}

	quotient, remainder := bits.Div64(high, low, divisor)
	if remainder != 0 {
		return saturatingAdd(quotient, 1)
	}

	return quotient
}

func saturatingAdd(value, increment uint64) uint64 {
	sum, carry := bits.Add64(value, increment, 0)
	if carry != 0 {
		return math.MaxUint64
	}

	return sum
}
