package auth

import (
	"fmt"
)

const (
	ClassLeafRevealed Class = iota
	ClassLeafUnrevealed
	ClassTreasury
)

const (
	secp256k1TypeURL = "/cosmos.crypto.secp256k1.PubKey"
	LeafAddressBytes = 20
)

type Class int

func ClassifyAccount(isModule bool, pubKeyTypeURL string, addressBytes int) Class {
	switch {
	case isModule:
		return ClassTreasury
	case addressBytes != LeafAddressBytes:
		return ClassTreasury
	case pubKeyTypeURL == secp256k1TypeURL:
		return ClassLeafRevealed
	case pubKeyTypeURL == "":
		return ClassLeafUnrevealed
	default:
		return ClassTreasury
	}
}

func CompressedSecp256k1(pubKey []byte) ([]byte, error) {
	if len(pubKey) != 33 {
		return nil, fmt.Errorf("secp256k1 pub key must be 33 bytes, got %d", len(pubKey))
	}

	if pubKey[0] != 0x02 && pubKey[0] != 0x03 {
		return nil, fmt.Errorf("invalid compressed prefix 0x%02x", pubKey[0])
	}

	out := make([]byte, 33)
	copy(out, pubKey)

	return out, nil
}
