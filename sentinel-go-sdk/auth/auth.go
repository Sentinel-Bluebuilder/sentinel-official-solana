package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/solana-foundation/solana-go/v2"
)

const (
	MetadataTTL     = time.Hour
	MetadataVersion = 1
)

//nolint:gochecknoglobals
var (
	DomainAPIRequest       = NewDomain("sentinel-dvpn/api-request/v1")
	DomainNodeMetadata     = NewDomain("sentinel-dvpn/node-metadata/v1")
	DomainProviderMetadata = NewDomain("sentinel-dvpn/provider-metadata/v1")
	DomainTicket           = NewDomain("sentinel-dvpn/ticket/v1")
	DomainTicketRequest    = NewDomain("sentinel-dvpn/ticket-request/v1")
)

var (
	ErrInvalidSignature      = errors.New("invalid signature")
	ErrMetadataExpired       = errors.New("metadata expired")
	ErrMetadataMismatch      = errors.New("metadata does not match the on-chain subject")
	ErrMetadataNotYetValid   = errors.New("metadata issued in the future")
	ErrMetadataWindowTooLong = errors.New("metadata validity window exceeds the ttl")
	ErrUnauthorized          = errors.New("signer is not the owner and holds no grant")
)

type Domain [32]byte

func NewDomain(text string) Domain {
	return sha256.Sum256([]byte(text))
}

type GrantSource interface {
	HasGrant(
		ctx context.Context,
		owner, signer solana.PublicKey,
		permission uint64,
		subjects ...solana.PublicKey,
	) (bool, error)
}

type MetadataCheck struct {
	Domain     Domain
	Expected   MetadataClaims
	Now        time.Time
	Permission uint64
	TTL        time.Duration
}

type MetadataClaims struct {
	ExpiresUnix int64
	IssuedUnix  int64
	Operator    solana.PublicKey
	Owner       solana.PublicKey
	ProgramID   solana.PublicKey
	Subject     solana.PublicKey
	Version     uint8
}

type SignedMessage struct {
	Payload   json.RawMessage  `json:"payload"`
	Signature []byte           `json:"signature"`
	Signer    solana.PublicKey `json:"signer"`
}

func (m SignedMessage) Decode(target any) error {
	if err := json.Unmarshal(m.Payload, target); err != nil {
		return fmt.Errorf("decoding payload: %w", err)
	}

	return nil
}

func (m SignedMessage) Verify(domain Domain) error {
	sum := sha256.Sum256(m.Payload)

	return Verify(m.Signer, domain, sum[:], m.Signature)
}

type SignedMessageCache[M any] struct {
	build   func(context.Context) (M, error)
	current atomic.Pointer[SignedMessage]
	sign    func(M) (SignedMessage, error)
}

func NewSignedMessageCache[M any](
	build func(context.Context) (M, error),
	sign func(M) (SignedMessage, error),
) *SignedMessageCache[M] {
	cache := &SignedMessageCache[M]{
		build: build,
		sign:  sign,
	}
	cache.current.Store(&SignedMessage{})

	return cache
}

func (c *SignedMessageCache[M]) Current() SignedMessage {
	return *c.current.Load()
}

func (c *SignedMessageCache[M]) Refresh(ctx context.Context) error {
	message, err := c.build(ctx)
	if err != nil {
		return err
	}

	signed, err := c.sign(message)
	if err != nil {
		return fmt.Errorf("signing metadata: %w", err)
	}

	c.current.Store(&signed)

	return nil
}

func Authorize(
	ctx context.Context,
	src GrantSource,
	owner, signer solana.PublicKey,
	permission uint64,
	subjects ...solana.PublicKey,
) error {
	if signer == owner {
		return nil
	}

	if src == nil {
		return ErrUnauthorized
	}

	ok, err := src.HasGrant(ctx, owner, signer, permission, subjects...)
	if err != nil {
		return fmt.Errorf("checking grant: %w", err)
	}

	if !ok {
		return fmt.Errorf("%w: %s for %s with permission %#x", ErrUnauthorized, signer, owner, permission)
	}

	return nil
}

func Sign(key solana.PrivateKey, domain Domain, msg []byte) []byte {
	return ed25519.Sign(ed25519.PrivateKey(key), append(domain[:], msg...))
}

func SignMessage(key solana.PrivateKey, domain Domain, value any) (SignedMessage, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return SignedMessage{}, fmt.Errorf("encoding payload: %w", err)
	}

	sum := sha256.Sum256(payload)

	return SignedMessage{
		Payload:   payload,
		Signature: Sign(key, domain, sum[:]),
		Signer:    key.PublicKey(),
	}, nil
}

func Verify(signer solana.PublicKey, domain Domain, msg, sig []byte) error {
	if len(sig) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(signer[:]), append(domain[:], msg...), sig) {
		return ErrInvalidSignature
	}

	return nil
}

func VerifyMetadata[M any](
	ctx context.Context,
	src GrantSource,
	signed SignedMessage,
	check MetadataCheck,
	claims func(M) MetadataClaims,
) (M, error) {
	var none M

	if err := signed.Verify(check.Domain); err != nil {
		return none, fmt.Errorf("verifying metadata signature: %w", err)
	}

	var metadata M
	if err := signed.Decode(&metadata); err != nil {
		return none, fmt.Errorf("decoding metadata: %w", err)
	}

	got := claims(metadata)
	expected := check.Expected

	switch {
	case got.Version != expected.Version:
		return none, fmt.Errorf("%w: version %d", ErrMetadataMismatch, got.Version)
	case got.Subject != expected.Subject ||
		got.Owner != expected.Owner ||
		got.ProgramID != expected.ProgramID ||
		got.Operator != expected.Operator:
		return none, ErrMetadataMismatch
	case check.Now.Unix() >= got.ExpiresUnix:
		return none, ErrMetadataExpired
	case got.IssuedUnix > check.Now.Add(MaxRequestSkew).Unix():
		return none, ErrMetadataNotYetValid
	case got.ExpiresUnix-got.IssuedUnix > int64(check.TTL.Seconds()):
		return none, ErrMetadataWindowTooLong
	}

	if err := Authorize(ctx, src, expected.Owner, signed.Signer, check.Permission, expected.Subject); err != nil {
		return none, fmt.Errorf("authorizing metadata signer: %w", err)
	}

	return metadata, nil
}
