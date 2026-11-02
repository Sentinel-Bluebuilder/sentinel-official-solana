package auth_test

import (
	"crypto/sha256"
	"encoding/binary"
	"net/http"
	"testing"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/auth"
)

func TestRequestMessage(t *testing.T) {
	t.Parallel()

	body := []byte(`{"a":1}`)
	nonce := auth.Nonce{1, 2, 3}

	got := auth.RequestMessage("POST", "/v1/sessions", body, 1_700_000_000, nonce)

	want := []byte{4}
	want = append(want, "POST"...)
	want = binary.LittleEndian.AppendUint16(want, 12)
	want = append(want, "/v1/sessions"...)
	want = binary.LittleEndian.AppendUint64(want, 1_700_000_000)
	want = append(want, nonce[:]...)

	sum := sha256.Sum256(body)
	want = append(want, sum[:]...)

	require.Equal(t, want, got)
}

func TestRequestSignatureFromHeaders_OldHeaderName_Rejected(t *testing.T) {
	t.Parallel()

	key, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	sig, err := auth.SignRequest(key, "GET", "/v1/sessions/abc", []byte(`{}`), time.Unix(1_700_000_000, 0))
	require.NoError(t, err)

	header := http.Header{}
	sig.Apply(header)
	signer := header.Get(auth.HeaderSigner)
	header.Del(auth.HeaderSigner)
	header.Set("X-Sentinel-Key", signer)

	_, err = auth.RequestSignatureFromHeaders(header)
	require.ErrorIs(t, err, auth.ErrInvalidSignatureHeader)
}

func TestVerifyRequest(t *testing.T) {
	t.Parallel()

	key, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{}`)

	sig, err := auth.SignRequest(key, "GET", "/v1/sessions/abc", body, now)
	require.NoError(t, err)

	header := http.Header{}
	sig.Apply(header)
	require.Equal(t, key.PublicKey().String(), header.Get(auth.HeaderSigner))

	parsed, err := auth.RequestSignatureFromHeaders(header)
	require.NoError(t, err)
	require.Equal(t, sig, parsed)

	seen := auth.NewNonceCache(auth.RequestNonceWindow)

	require.NoError(t, auth.VerifyRequest(parsed, "GET", "/v1/sessions/abc", body, now.Add(30*time.Second), seen))
	require.ErrorIs(
		t,
		auth.VerifyRequest(parsed, "GET", "/v1/sessions/abc", body, now.Add(31*time.Second), seen),
		auth.ErrNonceReplayed,
	)
	require.ErrorIs(
		t,
		auth.VerifyRequest(parsed, "GET", "/v1/sessions/abc", body, now.Add(2*time.Minute), auth.NewNonceCache(time.Minute)),
		auth.ErrTimestampOutOfRange,
	)
	require.ErrorIs(
		t,
		auth.VerifyRequest(parsed, "POST", "/v1/sessions/abc", body, now, auth.NewNonceCache(time.Minute)),
		auth.ErrInvalidSignature,
	)

	header.Del(auth.HeaderNonce)
	_, err = auth.RequestSignatureFromHeaders(header)
	require.ErrorIs(t, err, auth.ErrInvalidSignatureHeader)
}
