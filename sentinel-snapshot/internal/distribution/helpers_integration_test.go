//go:build integration

package distribution_test

import (
	"crypto/sha256"
)

func sha256Sum20(s string) []byte {
	sum := sha256.Sum256([]byte(s))

	return sum[:20]
}
