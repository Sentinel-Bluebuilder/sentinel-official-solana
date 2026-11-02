package auth_test

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
)

func TestDecodeTicket_RejectsTrailingBytes(t *testing.T) {
	t.Parallel()

	key, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	raw, err := base64.StdEncoding.DecodeString(auth.SignTicket(key, auth.Ticket{Version: auth.TicketVersion}).Encode())
	require.NoError(t, err)

	_, err = auth.DecodeTicket(base64.StdEncoding.EncodeToString(append(raw, 0)))
	require.ErrorIs(t, err, auth.ErrInvalidEncoding)
}

func TestDecodeTicketRequest_RejectsTrailingBytes(t *testing.T) {
	t.Parallel()

	key, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	request := auth.TicketRequest{Version: auth.TicketVersion}
	raw, err := base64.StdEncoding.DecodeString(auth.SignTicketRequest(key, request).Encode())
	require.NoError(t, err)

	_, err = auth.DecodeTicketRequest(base64.StdEncoding.EncodeToString(append(raw, 0)))
	require.ErrorIs(t, err, auth.ErrInvalidEncoding)
}

func TestSignedTicket_Encode(t *testing.T) {
	t.Parallel()

	key, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	ticket := auth.Ticket{
		DataCapBytes:   1_000,
		ExpiresUnix:    1_700_003_600,
		IssuedUnix:     1_700_000_000,
		MaxConnections: 2,
		Node:           solana.PublicKey{2},
		Nonce:          auth.Nonce{9},
		Provider:       solana.PublicKey{3},
		Subscription:   solana.PublicKey{4},
		User:           solana.PublicKey{1},
		Version:        auth.TicketVersion,
	}

	signed := auth.SignTicket(key, ticket)
	raw, err := base64.StdEncoding.DecodeString(signed.Encode())
	require.NoError(t, err)
	require.Len(t, raw, 266)
	require.Equal(t, byte(1), raw[0])
	require.Equal(t, ticket.Provider[:], raw[1:33])
	require.Equal(t, ticket.Node[:], raw[33:65])

	wantExpiry := []byte{0x00, 0xf1, 0x53, 0x65, 0, 0, 0, 0}
	require.Equal(t, wantExpiry, raw[129:137])
	require.Equal(t, key.PublicKey().Bytes(), raw[170:202])

	back, err := auth.DecodeTicket(signed.Encode())
	require.NoError(t, err)
	require.Equal(t, signed, back)
}

func TestSignedTicket_WireContract(t *testing.T) {
	t.Parallel()

	key := solana.PrivateKey(ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)))
	ticket := auth.Ticket{
		Version: 1, Provider: solana.PublicKey{1}, Node: solana.PublicKey{2}, User: key.PublicKey(),
		Subscription: solana.PublicKey{3}, IssuedUnix: 1700000000, ExpiresUnix: 1700003600,
		DataCapBytes: 1000, MaxConnections: 2, Nonce: auth.Nonce{4},
	}
	request := auth.TicketRequest{
		Version: 1, Subscription: solana.PublicKey{3}, Node: solana.PublicKey{2},
		Provider: solana.PublicKey{1}, RequestedUnix: 1700000000, Nonce: auth.Nonce{5},
	}

	require.Equal(t, "49730e256bcffe4482162bdabba065634bc3956731e6a0a6152209f67ff0ba19",
		fmt.Sprintf("%x", sha256.Sum256([]byte(auth.SignTicket(key, ticket).Encode()))))
	require.Equal(t, "754fc4b1d492e4ae66bd2e23d691dc211702fc4d3b52e8584e49b7bfca939167",
		fmt.Sprintf("%x", sha256.Sum256([]byte(auth.SignTicketRequest(key, request).Encode()))))
}

func TestTicket_JSONContract(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value any
		keys  []string
	}{
		{
			"ticket",
			auth.Ticket{},
			[]string{
				"version",
				"provider",
				"node",
				"user",
				"subscription",
				"issued_unix",
				"expires_unix",
				"data_cap_bytes",
				"max_connections",
				"nonce",
			},
		},
		{"signed ticket", auth.SignedTicket{}, []string{"ticket", "signer", "signature"}},
		{"request", auth.TicketRequest{}, []string{"version", "subscription", "node", "provider", "requested_unix", "nonce"}},
		{"signed request", auth.SignedTicketRequest{}, []string{"request", "signer", "signature"}},
		{"request signature", auth.RequestSignature{}, []string{"nonce", "signature", "signed_unix", "signer"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tc.value)
			require.NoError(t, err)

			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(data, &fields))
			require.Len(t, fields, len(tc.keys))

			for _, key := range tc.keys {
				require.Contains(t, fields, key)
			}
		})
	}
}

func TestVerifyTicket(t *testing.T) {
	t.Parallel()

	providerKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	node := solana.PublicKey{2}
	now := time.Unix(1_700_000_100, 0)

	ticket := auth.Ticket{
		ExpiresUnix: 1_700_003_600,
		IssuedUnix:  1_700_000_000,
		Node:        node,
		Provider:    providerKey.PublicKey(),
		Version:     auth.TicketVersion,
	}

	signed := auth.SignTicket(providerKey, ticket)

	require.NoError(t, auth.VerifyTicket(t.Context(), nil, signed, providerKey.PublicKey(), node, now))
	require.ErrorIs(
		t,
		auth.VerifyTicket(t.Context(), nil, signed, providerKey.PublicKey(), node, time.Unix(1_700_003_600, 0)),
		auth.ErrTicketExpired,
	)
	require.ErrorIs(
		t,
		auth.VerifyTicket(t.Context(), nil, signed, providerKey.PublicKey(), providerKey.PublicKey(), now),
		auth.ErrTicketNodeMismatch,
	)
	require.ErrorIs(t, auth.VerifyTicket(t.Context(), nil, signed, node, node, now), auth.ErrUnauthorized)

	signed.Signature[0]++
	require.ErrorIs(
		t,
		auth.VerifyTicket(t.Context(), nil, signed, providerKey.PublicKey(), node, now),
		auth.ErrInvalidSignature,
	)
}

func TestVerifyTicketRequest(t *testing.T) {
	t.Parallel()

	userKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	now := time.Unix(1_700_000_000, 0)
	request := auth.TicketRequest{
		Node:          solana.PublicKey{2},
		Nonce:         auth.Nonce{1},
		Provider:      solana.PublicKey{3},
		RequestedUnix: now.Unix(),
		Subscription:  solana.PublicKey{4},
		Version:       auth.TicketVersion,
	}

	signedRequest := auth.SignTicketRequest(userKey, request)
	raw, err := base64.StdEncoding.DecodeString(signedRequest.Encode())
	require.NoError(t, err)
	require.Len(t, raw, 217)

	back, err := auth.DecodeTicketRequest(signedRequest.Encode())
	require.NoError(t, err)
	require.Equal(t, signedRequest, back)

	seen := auth.NewNonceCache(time.Minute)
	require.NoError(
		t,
		auth.VerifyTicketRequest(t.Context(), nil, signedRequest, userKey.PublicKey(), now.Add(time.Minute), seen),
	)
	require.ErrorIs(
		t,
		auth.VerifyTicketRequest(t.Context(), nil, signedRequest, userKey.PublicKey(), now.Add(time.Minute), seen),
		auth.ErrNonceReplayed,
	)
	require.ErrorIs(
		t,
		auth.VerifyTicketRequest(
			t.Context(),
			nil,
			signedRequest,
			userKey.PublicKey(),
			now.Add(6*time.Minute),
			auth.NewNonceCache(time.Minute),
		),
		auth.ErrTicketRequestStale,
	)
	require.ErrorIs(
		t,
		auth.VerifyTicketRequest(t.Context(), nil, signedRequest, request.Node, now, auth.NewNonceCache(time.Minute)),
		auth.ErrUnauthorized,
	)
}
