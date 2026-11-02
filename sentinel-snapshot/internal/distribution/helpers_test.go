package distribution_test

import (
	"cosmossdk.io/math"
)

func dec(s string) math.LegacyDec { return math.LegacyMustNewDecFromStr(s) }
