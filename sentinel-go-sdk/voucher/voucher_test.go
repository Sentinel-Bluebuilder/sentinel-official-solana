package voucher_test

import (
	"encoding/hex"
	"testing"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/voucher"
)

const (
	pinChannelDomainHex  = "0a34aa1e233037f6558a364baf39c430236bdfc99943bdeec1bae0ee7222b354"
	pinLeaseDomainHex    = "98a66b3e1f237c2e538a89bff2833c604b340b70023ec1403659ae9eb559d1c3"
	pinSubjectHex        = "0101010101010101010101010101010101010101010101010101010101010101"
	pinChannelMessageHex = pinChannelDomainHex + pinSubjectHex + "00f153650000000040420f0000000000"
	pinLeaseMessageHex   = pinLeaseDomainHex + pinSubjectHex + "00f153650000000040420f0000000000"
	pinOpenedAt          = 1_700_000_000
	pinCumulative        = 1_000_000
)

const (
	vectorMalleatedHex = "530eb49aae917f88d3a9a47768a68da505e66d52dbf8c8a6ce3dd03e695dab05" +
		"614231154a7ce766d33e4cf25fe6f81791164724c399be07385f22446bc36011"
	vectorMessageHex = pinChannelDomainHex +
		"0200000000000000000000000000000000000000000000000000000000000000" +
		"00f1536500000000f401000000000000"
	vectorPublicKeyHex = "ea4a6c63e29c520abef5507b132ec5f9954776aebebe7b92421eea691446d22c"
	vectorSignatureHex = "530eb49aae917f88d3a9a47768a68da505e66d52dbf8c8a6ce3dd03e695dab05" +
		"746e3bb82f19d50efda1544f81ec190391164724c399be07385f22446bc36001"
)

func TestDomainChannel_Pinned(t *testing.T) {
	t.Parallel()

	require.Equal(t, mustHex(t, pinChannelDomainHex), voucher.DomainChannel[:])
}

func TestDomainLease_Pinned(t *testing.T) {
	t.Parallel()

	require.Equal(t, mustHex(t, pinLeaseDomainHex), voucher.DomainLease[:])
}

func TestMessage_Layout(t *testing.T) {
	t.Parallel()

	subject := solana.PublicKeyFromBytes(mustHex(t, pinSubjectHex))

	channel := voucher.ChannelMessage(subject, pinOpenedAt, pinCumulative)
	require.Equal(t, mustHex(t, pinChannelMessageHex), channel[:])

	lease := voucher.LeaseMessage(subject, pinOpenedAt, pinCumulative)
	require.Equal(t, mustHex(t, pinLeaseMessageHex), lease[:])
}

func TestSign_RoundTrip(t *testing.T) {
	t.Parallel()

	key, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	subject := solana.PublicKey{1}
	message := voucher.ChannelMessage(subject, 1_700_000_000, 42)

	signed := voucher.Sign(key, message)
	require.EqualValues(t, 42, signed.CumulativeAmount)
	require.NoError(t, voucher.Verify(key.PublicKey(), message, signed.Signature[:]))

	message[79]++
	require.ErrorIs(t, voucher.Verify(key.PublicKey(), message, signed.Signature[:]), voucher.ErrInvalidSignature)
}

func TestVerify_SignatureVectors(t *testing.T) {
	t.Parallel()

	pub := solana.PublicKeyFromBytes(mustHex(t, vectorPublicKeyHex))

	var message voucher.Message

	copy(message[:], mustHex(t, vectorMessageHex))

	require.NoError(t, voucher.Verify(pub, message, mustHex(t, vectorSignatureHex)))
	require.ErrorIs(t, voucher.Verify(pub, message, mustHex(t, vectorMalleatedHex)), voucher.ErrInvalidSignature)
}

func mustHex(t *testing.T, text string) []byte {
	t.Helper()

	raw, err := hex.DecodeString(text)
	require.NoError(t, err)

	return raw
}
