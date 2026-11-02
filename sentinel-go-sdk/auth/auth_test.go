package auth_test

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
)

func TestAuthorize(t *testing.T) {
	t.Parallel()

	owner := solana.PublicKey{1}
	operator := solana.PublicKey{5}
	stranger := solana.PublicKey{2}

	perm := program.PermsNode
	src := fakeGrantSource{operator: {permission: perm, subjects: []solana.PublicKey{{9}}}}

	require.NoError(t, auth.Authorize(t.Context(), src, owner, owner, perm))
	require.NoError(t, auth.Authorize(t.Context(), src, owner, operator, perm, solana.PublicKey{9}))
	require.ErrorIs(t, auth.Authorize(t.Context(), src, owner, stranger, perm), auth.ErrUnauthorized)
	require.ErrorIs(t, auth.Authorize(t.Context(), nil, owner, operator, perm), auth.ErrUnauthorized)
}

func TestNewDomain(t *testing.T) {
	t.Parallel()

	for text, domain := range map[string]auth.Domain{
		"sentinel-dvpn/api-request/v1":       auth.DomainAPIRequest,
		"sentinel-dvpn/node-metadata/v1":     auth.DomainNodeMetadata,
		"sentinel-dvpn/provider-metadata/v1": auth.DomainProviderMetadata,
		"sentinel-dvpn/ticket/v1":            auth.DomainTicket,
		"sentinel-dvpn/ticket-request/v1":    auth.DomainTicketRequest,
	} {
		require.Equal(t, auth.Domain(sha256.Sum256([]byte(text))), domain, text)
	}

	require.NotEqual(t, auth.NewDomain("a"), auth.NewDomain("b"))
	require.NotPanics(t, func() { auth.NewDomain("this string is definitely longer than thirty-two bytes") })
}

func TestSignMessage(t *testing.T) {
	t.Parallel()

	key, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	input := payload{
		Name:  "n",
		Value: 7,
	}

	signed, err := auth.SignMessage(key, auth.DomainNodeMetadata, input)
	require.NoError(t, err)
	require.Equal(t, key.PublicKey(), signed.Signer)
	require.NoError(t, signed.Verify(auth.DomainNodeMetadata))

	var out payload
	require.NoError(t, signed.Decode(&out))
	require.Equal(t, input, out)

	signed.Payload = []byte(`{"name":"n","value":8}`)
	require.ErrorIs(t, signed.Verify(auth.DomainNodeMetadata), auth.ErrInvalidSignature)
}

func TestVerify(t *testing.T) {
	t.Parallel()

	key, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	msg := []byte("hello")
	sig := auth.Sign(key, auth.DomainNodeMetadata, msg)

	require.NoError(t, auth.Verify(key.PublicKey(), auth.DomainNodeMetadata, msg, sig))
	require.ErrorIs(t, auth.Verify(key.PublicKey(), auth.DomainProviderMetadata, msg, sig), auth.ErrInvalidSignature)
	require.ErrorIs(
		t,
		auth.Verify(key.PublicKey(), auth.DomainNodeMetadata, []byte("hellp"), sig),
		auth.ErrInvalidSignature,
	)
}

func TestVerifyMetadata(t *testing.T) {
	t.Parallel()

	operatorKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	owner := solana.PublicKey{1}
	subject := solana.PublicKey{2}
	programID := solana.MustPublicKeyFromBase58("SentineLDVPN1111111111111111111111111111111")
	now := time.Unix(1_700_000_000, 0)

	metadata := metadataFixture{
		ExpiresAt: now.Add(auth.MetadataTTL).Unix(),
		IssuedAt:  now.Unix(),
		Operator:  operatorKey.PublicKey(),
		Owner:     owner,
		ProgramID: programID,
		Subject:   subject,
		Version:   auth.MetadataVersion,
	}

	signed, err := auth.SignMessage(operatorKey, auth.DomainNodeMetadata, metadata)
	require.NoError(t, err)

	want := auth.MetadataClaims{
		Operator:  operatorKey.PublicKey(),
		Owner:     owner,
		ProgramID: programID,
		Subject:   subject,
		Version:   auth.MetadataVersion,
	}
	check := auth.MetadataCheck{
		Domain:     auth.DomainNodeMetadata,
		Expected:   want,
		Now:        now.Add(time.Minute),
		Permission: program.PermSignNodeMetadata,
		TTL:        auth.MetadataTTL,
	}
	src := fakeGrantSource{operatorKey.PublicKey(): {
		permission: program.PermSignNodeMetadata,
		subjects:   []solana.PublicKey{subject},
	}}

	got, err := auth.VerifyMetadata(t.Context(), src, signed, check, claimsFixture)
	require.NoError(t, err)
	require.Equal(t, metadata, got)

	expired := check
	expired.Now = now.Add(2 * time.Hour)
	_, err = auth.VerifyMetadata(t.Context(), src, signed, expired, claimsFixture)
	require.ErrorIs(t, err, auth.ErrMetadataExpired)

	mismatch := check
	mismatch.Expected.Subject = owner
	_, err = auth.VerifyMetadata(t.Context(), src, signed, mismatch, claimsFixture)
	require.ErrorIs(t, err, auth.ErrMetadataMismatch)

	wrongVersion := check
	wrongVersion.Expected.Version = 2
	_, err = auth.VerifyMetadata(t.Context(), src, signed, wrongVersion, claimsFixture)
	require.ErrorIs(t, err, auth.ErrMetadataMismatch)

	wrongDomain := check
	wrongDomain.Domain = auth.DomainProviderMetadata
	_, err = auth.VerifyMetadata(t.Context(), src, signed, wrongDomain, claimsFixture)
	require.ErrorIs(t, err, auth.ErrInvalidSignature)

	_, err = auth.VerifyMetadata(t.Context(), fakeGrantSource{}, signed, check, claimsFixture)
	require.ErrorIs(t, err, auth.ErrUnauthorized)
}

func TestVerifyMetadata_FutureAndWindow(t *testing.T) {
	t.Parallel()

	operatorKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	owner := solana.PublicKey{1}
	now := time.Unix(1_700_000_000, 0)
	src := fakeGrantSource{operatorKey.PublicKey(): {
		permission: program.PermSignProviderMetadata,
		subjects:   []solana.PublicKey{owner},
	}}
	want := auth.MetadataClaims{
		Operator: operatorKey.PublicKey(),
		Owner:    owner,
		Subject:  owner,
		Version:  auth.MetadataVersion,
	}
	check := auth.MetadataCheck{
		Domain:     auth.DomainProviderMetadata,
		Expected:   want,
		Now:        now,
		Permission: program.PermSignProviderMetadata,
		TTL:        auth.MetadataTTL,
	}

	future := metadataFixture{
		ExpiresAt: now.Add(auth.MetadataTTL).Unix(),
		IssuedAt:  now.Add(2 * auth.MaxRequestSkew).Unix(),
		Operator:  operatorKey.PublicKey(),
		Owner:     owner,
		Subject:   owner,
		Version:   auth.MetadataVersion,
	}

	futureSigned, err := auth.SignMessage(operatorKey, auth.DomainProviderMetadata, future)
	require.NoError(t, err)

	_, err = auth.VerifyMetadata(t.Context(), src, futureSigned, check, claimsFixture)
	require.ErrorIs(t, err, auth.ErrMetadataNotYetValid)

	overlong := future
	overlong.IssuedAt = now.Unix()
	overlong.ExpiresAt = now.Add(2 * auth.MetadataTTL).Unix()

	overlongSigned, err := auth.SignMessage(operatorKey, auth.DomainProviderMetadata, overlong)
	require.NoError(t, err)

	_, err = auth.VerifyMetadata(t.Context(), src, overlongSigned, check, claimsFixture)
	require.ErrorIs(t, err, auth.ErrMetadataWindowTooLong)
}
