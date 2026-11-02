package staking

import (
	"fmt"

	"cosmossdk.io/math"
)

func TokensFromShares(shares math.LegacyDec, validatorTokens math.Int, validatorShares math.LegacyDec) math.Int {
	return shares.MulInt(validatorTokens).Quo(validatorShares).TruncateInt()
}

func ValidatorPoolDust(validatorTokens math.Int, delegatedTokens []math.Int) (math.Int, error) {
	sum := math.ZeroInt()
	for _, tokens := range delegatedTokens {
		sum = sum.Add(tokens)
	}

	if validatorTokens.LT(sum) {
		return math.Int{}, fmt.Errorf(
			"negative pool dust: validator tokens %s < Σ credited delegations %s",
			validatorTokens, sum,
		)
	}

	return validatorTokens.Sub(sum), nil
}
