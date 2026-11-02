package voucher

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/solana-foundation/solana-go/v2"

	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const MessageBytes = 80

//nolint:gochecknoglobals
var (
	DomainChannel = auth.NewDomain("sentinel-dvpn/channel-voucher/v1")
	DomainLease   = auth.NewDomain("sentinel-dvpn/lease-voucher/v1")
)

var ErrInvalidSignature = errors.New("invalid voucher signature")

type Message [MessageBytes]byte

func ChannelMessage(subject solana.PublicKey, openedAt int64, cumulative uint64) Message {
	return build(DomainChannel, subject, openedAt, cumulative)
}

func LeaseMessage(subject solana.PublicKey, openedAt int64, cumulative uint64) Message {
	return build(DomainLease, subject, openedAt, cumulative)
}

func Sign(key solana.PrivateKey, message Message) dvpn.Voucher {
	signed := dvpn.Voucher{CumulativeAmount: binary.LittleEndian.Uint64(message[72:80])}

	copy(signed.Signature[:], ed25519.Sign(ed25519.PrivateKey(key), message[:]))

	return signed
}

func Verify(signer solana.PublicKey, message Message, sig []byte) error {
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature is %d bytes", ErrInvalidSignature, len(sig))
	}

	if !ed25519.Verify(ed25519.PublicKey(signer[:]), message[:], sig) {
		return ErrInvalidSignature
	}

	return nil
}

func build(domain auth.Domain, subject solana.PublicKey, openedAt int64, cumulative uint64) Message {
	var message Message

	copy(message[:32], domain[:])
	copy(message[32:64], subject[:])
	binary.LittleEndian.PutUint64(message[64:72], uint64(openedAt))
	binary.LittleEndian.PutUint64(message[72:80], cumulative)

	return message
}
