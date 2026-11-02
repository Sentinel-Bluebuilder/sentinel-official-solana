package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	bin "github.com/gagliardetto/binary"
	"github.com/jellydator/ttlcache/v3"
	"github.com/solana-foundation/solana-go/v2"

	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
)

const (
	TicketRequestMaxAge = 5 * time.Minute
	TicketVersion       = 1
)

var (
	ErrInvalidEncoding          = errors.New("invalid encoding")
	ErrTicketExpired            = errors.New("ticket expired")
	ErrTicketNodeMismatch       = errors.New("ticket is for a different node")
	ErrTicketRequestStale       = errors.New("ticket request is stale")
	ErrUnsupportedTicketVersion = errors.New("unsupported ticket version")
)

//nolint:decorder
type SignedTicket struct {
	Ticket    Ticket                       `json:"ticket"`
	Signer    solana.PublicKey             `json:"signer"`
	Signature [solana.SignatureLength]byte `json:"signature"`
}

func (st SignedTicket) Encode() string {
	return base64.StdEncoding.EncodeToString(encodeBorsh(st))
}

//nolint:decorder
type SignedTicketRequest struct {
	Request   TicketRequest                `json:"request"`
	Signer    solana.PublicKey             `json:"signer"`
	Signature [solana.SignatureLength]byte `json:"signature"`
}

func (sr SignedTicketRequest) Encode() string {
	return base64.StdEncoding.EncodeToString(encodeBorsh(sr))
}

//nolint:decorder
type Ticket struct {
	Version        uint8            `json:"version"`
	Provider       solana.PublicKey `json:"provider"`
	Node           solana.PublicKey `json:"node"`
	User           solana.PublicKey `json:"user"`
	Subscription   solana.PublicKey `json:"subscription"`
	IssuedUnix     int64            `json:"issued_unix"`
	ExpiresUnix    int64            `json:"expires_unix"`
	DataCapBytes   uint64           `json:"data_cap_bytes"`
	MaxConnections uint8            `json:"max_connections"`
	Nonce          Nonce            `json:"nonce"`
}

func (t Ticket) Hash() [32]byte {
	return sha256.Sum256(encodeBorsh(t))
}

//nolint:decorder
type TicketRequest struct {
	Version       uint8            `json:"version"`
	Subscription  solana.PublicKey `json:"subscription"`
	Node          solana.PublicKey `json:"node"`
	Provider      solana.PublicKey `json:"provider"`
	RequestedUnix int64            `json:"requested_unix"`
	Nonce         Nonce            `json:"nonce"`
}

func DecodeTicket(encoded string) (SignedTicket, error) {
	var signed SignedTicket
	if err := decodeBase64Borsh(encoded, &signed); err != nil {
		return signed, fmt.Errorf("decoding ticket: %w", err)
	}

	if signed.Ticket.Version != TicketVersion {
		return signed, fmt.Errorf("%w: %d", ErrUnsupportedTicketVersion, signed.Ticket.Version)
	}

	return signed, nil
}

func DecodeTicketRequest(encoded string) (SignedTicketRequest, error) {
	var signed SignedTicketRequest
	if err := decodeBase64Borsh(encoded, &signed); err != nil {
		return signed, fmt.Errorf("decoding ticket request: %w", err)
	}

	if signed.Request.Version != TicketVersion {
		return signed, fmt.Errorf("%w: %d", ErrUnsupportedTicketVersion, signed.Request.Version)
	}

	return signed, nil
}

func SignTicket(key solana.PrivateKey, t Ticket) SignedTicket {
	signed := SignedTicket{
		Signer: key.PublicKey(),
		Ticket: t,
	}

	copy(signed.Signature[:], Sign(key, DomainTicket, encodeBorsh(t)))

	return signed
}

func SignTicketRequest(key solana.PrivateKey, request TicketRequest) SignedTicketRequest {
	signed := SignedTicketRequest{
		Request: request,
		Signer:  key.PublicKey(),
	}

	copy(signed.Signature[:], Sign(key, DomainTicketRequest, encodeBorsh(request)))

	return signed
}

func VerifyTicket(
	ctx context.Context,
	src GrantSource,
	signed SignedTicket,
	providerOwner,
	node solana.PublicKey,
	now time.Time,
) error {
	if err := Verify(signed.Signer, DomainTicket, encodeBorsh(signed.Ticket), signed.Signature[:]); err != nil {
		return err
	}

	if err := Authorize(
		ctx,
		src,
		providerOwner,
		signed.Signer,
		program.PermIssueTickets,
		signed.Ticket.Provider,
		node,
	); err != nil {
		return err
	}

	if signed.Ticket.Node != node {
		return ErrTicketNodeMismatch
	}

	if now.Unix() >= signed.Ticket.ExpiresUnix {
		return ErrTicketExpired
	}

	return nil
}

func VerifyTicketRequest(
	ctx context.Context,
	src GrantSource,
	signed SignedTicketRequest,
	subscriptionOwner solana.PublicKey,
	now time.Time,
	seen *ttlcache.Cache[Nonce, struct{}],
) error {
	if err := Verify(signed.Signer, DomainTicketRequest, encodeBorsh(signed.Request), signed.Signature[:]); err != nil {
		return err
	}

	if err := Authorize(
		ctx,
		src,
		subscriptionOwner,
		signed.Signer,
		program.PermUseSubscription,
		signed.Request.Subscription,
		signed.Request.Provider,
	); err != nil {
		return err
	}

	age := now.Unix() - signed.Request.RequestedUnix
	if age > int64(TicketRequestMaxAge.Seconds()) || -age > int64(MaxRequestSkew.Seconds()) {
		return ErrTicketRequestStale
	}

	ttlOpt := ttlcache.WithTTL[Nonce, struct{}](ttlcache.DefaultTTL)
	if _, found := seen.GetOrSet(signed.Request.Nonce, struct{}{}, ttlOpt); found {
		return ErrNonceReplayed
	}

	return nil
}

func decodeBase64Borsh(encoded string, target any) error {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("decoding base64: %w", err)
	}

	decoder := bin.NewBorshDecoder(raw)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decoding borsh: %w", err)
	}

	if n := decoder.Remaining(); n > 0 {
		return fmt.Errorf("%w: %d trailing bytes", ErrInvalidEncoding, n)
	}

	return nil
}

func encodeBorsh(value any) []byte {
	var buf bytes.Buffer
	if err := bin.NewBorshEncoder(&buf).Encode(value); err != nil {
		panic(err)
	}

	return buf.Bytes()
}
