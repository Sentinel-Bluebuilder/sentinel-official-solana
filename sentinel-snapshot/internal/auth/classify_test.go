package auth_test

import (
	"testing"

	"github.com/sentinel-official/sentinel-snapshot/internal/auth"
)

func TestClassifyAccount(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		isModule      bool
		pubKeyTypeURL string
		addressBytes  int
		want          auth.Class
	}{
		{"revealed secp256k1, 20 bytes, non-module", false, "/cosmos.crypto.secp256k1.PubKey", 20, auth.ClassLeafRevealed},
		{"never revealed, 20 bytes, non-module", false, "", 20, auth.ClassLeafUnrevealed},
		{"module account", true, "", 20, auth.ClassTreasury},
		{"multisig", false, "/cosmos.crypto.multisig.LegacyAminoPubKey", 20, auth.ClassTreasury},
		{"ed25519", false, "/cosmos.crypto.ed25519.PubKey", 20, auth.ClassTreasury},
		{"32-byte address, never revealed", false, "", 32, auth.ClassTreasury},
		{"32-byte address, revealed secp256k1", false, "/cosmos.crypto.secp256k1.PubKey", 32, auth.ClassTreasury},
		{"module account, 32-byte address", true, "", 32, auth.ClassTreasury},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := auth.ClassifyAccount(c.isModule, c.pubKeyTypeURL, c.addressBytes); got != c.want {
				t.Fatalf("ClassifyAccount(%v,%q,%d) = %v, want %v", c.isModule, c.pubKeyTypeURL, c.addressBytes, got, c.want)
			}
		})
	}
}
