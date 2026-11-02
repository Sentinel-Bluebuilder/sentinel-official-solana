package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/solana-foundation/solana-go/v2"
)

const (
	HeaderNonce     = "X-Sentinel-Nonce"
	HeaderSignature = "X-Sentinel-Signature"
	HeaderSigner    = "X-Sentinel-Signer"
	HeaderTimestamp = "X-Sentinel-Timestamp"
)

const (
	MaxRequestSkew     = 60 * time.Second
	RequestNonceWindow = 10 * time.Minute
)

var (
	ErrInvalidSignatureHeader = errors.New("invalid signature header")
	ErrNonceReplayed          = errors.New("request nonce already seen")
	ErrTimestampOutOfRange    = errors.New("request timestamp outside the allowed skew")
)

type Nonce [16]byte

func NewNonce() (Nonce, error) {
	var nonce Nonce
	if _, err := rand.Read(nonce[:]); err != nil {
		return Nonce{}, fmt.Errorf("generating nonce: %w", err)
	}

	return nonce, nil
}

type RequestSignature struct {
	Nonce      Nonce            `json:"nonce"`
	Signature  []byte           `json:"signature"`
	SignedUnix int64            `json:"signed_unix"`
	Signer     solana.PublicKey `json:"signer"`
}

func (s RequestSignature) Apply(header http.Header) {
	header.Set(HeaderSigner, s.Signer.String())
	header.Set(HeaderNonce, base64.StdEncoding.EncodeToString(s.Nonce[:]))
	header.Set(HeaderSignature, base64.StdEncoding.EncodeToString(s.Signature))
	header.Set(HeaderTimestamp, strconv.FormatInt(s.SignedUnix, 10))
}

func NewNonceCache(window time.Duration) *ttlcache.Cache[Nonce, struct{}] {
	cache := ttlcache.New(ttlcache.WithTTL[Nonce, struct{}](window), ttlcache.WithDisableTouchOnHit[Nonce, struct{}]())

	go cache.Start()

	return cache
}

func RequestMessage(method, path string, body []byte, signedUnix int64, nonce Nonce) []byte {
	out := make([]byte, 0, 1+len(method)+2+len(path)+8+len(nonce)+sha256.Size)
	out = append(out, byte(min(len(method), math.MaxUint8)))
	out = append(out, method...)
	out = binary.LittleEndian.AppendUint16(out, uint16(min(len(path), math.MaxUint16)))
	out = append(out, path...)
	out = binary.LittleEndian.AppendUint64(out, uint64(signedUnix))
	out = append(out, nonce[:]...)

	sum := sha256.Sum256(body)

	return append(out, sum[:]...)
}

func RequestSignatureFromHeaders(header http.Header) (RequestSignature, error) {
	var sig RequestSignature

	for _, name := range []string{HeaderSigner, HeaderNonce, HeaderSignature, HeaderTimestamp} {
		if header.Get(name) == "" {
			return sig, fmt.Errorf("%w: %s", ErrInvalidSignatureHeader, name)
		}
	}

	key, err := solana.PublicKeyFromBase58(header.Get(HeaderSigner))
	if err != nil {
		return sig, fmt.Errorf("%w: %s: %w", ErrInvalidSignatureHeader, HeaderSigner, err)
	}

	nonce, err := base64.StdEncoding.DecodeString(header.Get(HeaderNonce))
	if err != nil || len(nonce) != len(Nonce{}) {
		return sig, fmt.Errorf("%w: %s", ErrInvalidSignatureHeader, HeaderNonce)
	}

	signature, err := base64.StdEncoding.DecodeString(header.Get(HeaderSignature))
	if err != nil {
		return sig, fmt.Errorf("%w: %s", ErrInvalidSignatureHeader, HeaderSignature)
	}

	signedUnix, err := strconv.ParseInt(header.Get(HeaderTimestamp), 10, 64)
	if err != nil {
		return sig, fmt.Errorf("%w: %s", ErrInvalidSignatureHeader, HeaderTimestamp)
	}

	sig.Signer = key
	sig.Signature = signature
	sig.SignedUnix = signedUnix
	copy(sig.Nonce[:], nonce)

	return sig, nil
}

func SignRequest(key solana.PrivateKey, method, path string, body []byte, now time.Time) (RequestSignature, error) {
	nonce, err := NewNonce()
	if err != nil {
		return RequestSignature{}, err
	}

	signedUnix := now.Unix()

	return RequestSignature{
		Nonce:      nonce,
		Signature:  Sign(key, DomainAPIRequest, RequestMessage(method, path, body, signedUnix, nonce)),
		SignedUnix: signedUnix,
		Signer:     key.PublicKey(),
	}, nil
}

func VerifyRequest(
	sig RequestSignature,
	method,
	path string,
	body []byte,
	now time.Time,
	seen *ttlcache.Cache[Nonce, struct{}],
) error {
	skew := now.Unix() - sig.SignedUnix
	if skew > int64(MaxRequestSkew.Seconds()) || -skew > int64(MaxRequestSkew.Seconds()) {
		return ErrTimestampOutOfRange
	}

	if err := Verify(
		sig.Signer,
		DomainAPIRequest,
		RequestMessage(method, path, body, sig.SignedUnix, sig.Nonce),
		sig.Signature,
	); err != nil {
		return err
	}

	if _, found := seen.GetOrSet(sig.Nonce, struct{}{}, ttlcache.WithTTL[Nonce, struct{}](ttlcache.DefaultTTL)); found {
		return ErrNonceReplayed
	}

	return nil
}
