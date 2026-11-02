//go:build linux

package node_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/node"
	"github.com/sentinel-official/sentinel-dvpnd/internal/store"
	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
	"github.com/sentinel-official/sentinel-go-sdk/v3/voucher"
	"github.com/sentinel-official/sentinel-go-sdk/v3/wireguard"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const (
	channelEscrow           = 1_000_000_000
	channelOpenedUnix int64 = 1_700_000_000
)

func channelFixture(
	t *testing.T,
	r *node.Runtime,
	home testutil.Home,
	owner solana.PrivateKey,
	opener solana.PublicKey,
	voucherSigner solana.PublicKey,
	mode uint8,
) (solana.PublicKey, dvpn.Channel) {
	t.Helper()

	var id [32]uint8

	id[0] = 9
	address, _, err := dvpn.FindChannelAddress(home.ProgramClient.ProgramID, owner.PublicKey(), id)
	require.NoError(t, err)

	_, chainNode := node.ChainState(r)
	channel := dvpn.Channel{
		ChannelID:     id,
		Escrow:        channelEscrow,
		Mode:          mode,
		Node:          home.NodeAddress,
		NodeOpenedAt:  chainNode.OpenedAt,
		OpenedAt:      channelOpenedUnix,
		Opener:        opener,
		Owner:         owner.PublicKey(),
		RatePerHour:   chainNode.ChannelPricePerHour,
		Status:        dvpn.PositionStatusOpen,
		VoucherSigner: voucherSigner,
	}

	return address, channel
}

func loadSettled(t *testing.T, home testutil.Home, channel solana.PublicKey) uint64 {
	t.Helper()

	var position node.Position
	require.NoError(t, home.DB.WithContext(t.Context()).First(&position, "address = ?", channel.String()).Error)

	return position.Settled
}

func newRuntime(t *testing.T) (*node.Runtime, testutil.Home, *sdktest.FakeState) {
	t.Helper()

	return newRuntimeWithRegistry(t, prometheus.NewRegistry())
}

func newRuntimeWithRegistry(
	t *testing.T, reg prometheus.Registerer,
) (*node.Runtime, testutil.Home, *sdktest.FakeState) {
	t.Helper()
	fakeRPC := sdktest.NewFakeRPC(t)
	home := testutil.NewNodeHome(t, fakeRPC)
	r, err := node.New(
		home.Loaded, home.DB, home.DaemonKey, home.ProgramClient, home.Sender, zerolog.Nop(), reg,
	)
	require.NoError(t, err)
	require.NoError(t, store.Migrate(t.Context(), home.DB, r.Models()...))
	require.NoError(t, node.SyncChain(t.Context(), r))

	state := sdktest.NewFakeState(wireguard.Name)
	node.SetService(r, sdktest.NewFakeServer(state))

	return r, home, state
}

func newSignedContext(t *testing.T, signer solana.PublicKey) *gin.Context {
	t.Helper()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
	ctx.Set("sentinel.signer", signer)

	return ctx
}

func peerRequestFixture() nodeapi.PeerRequest {
	return nodeapi.PeerRequest{Name: wireguard.Name, Payload: json.RawMessage(`{"public_key":"client"}`)}
}

func resolveAccounts(sentTx *solana.Transaction) ([]solana.PublicKey, error) {
	instruction := sentTx.Message.Instructions[0]

	metas, err := instruction.ResolveInstructionAccounts(&sentTx.Message)
	if err != nil {
		return nil, fmt.Errorf("resolving instruction accounts: %w", err)
	}

	accounts := make([]solana.PublicKey, 0, len(metas))
	for _, meta := range metas {
		accounts = append(accounts, meta.PublicKey)
	}

	return accounts, nil
}

func seedChannel(
	t *testing.T, r *node.Runtime, home testutil.Home, owner solana.PrivateKey, mode uint8,
) solana.PublicKey {
	t.Helper()

	return seedChannelOwnedBy(t, r, home, owner, mode, home.ProgramClient.ProgramID)
}

func seedChannelAccount(
	t *testing.T,
	r *node.Runtime,
	home testutil.Home,
	owner solana.PrivateKey,
	opener solana.PublicKey,
	voucherSigner solana.PublicKey,
	mode uint8,
	ownerProgram solana.PublicKey,
) solana.PublicKey {
	t.Helper()

	address, channel := channelFixture(t, r, home, owner, opener, voucherSigner, mode)
	home.RPC.SetAccount(address, sdktest.Account{
		Data:  sdktest.EncodeAccount(dvpn.ChannelAccountDiscriminator, channel),
		Owner: ownerProgram,
	})

	return address
}

func seedChannelOwnedBy(
	t *testing.T,
	r *node.Runtime,
	home testutil.Home,
	owner solana.PrivateKey,
	mode uint8,
	ownerProgram solana.PublicKey,
) solana.PublicKey {
	t.Helper()

	return seedChannelAccount(t, r, home, owner, owner.PublicKey(), owner.PublicKey(), mode, ownerProgram)
}

func seedDelegatedChannel(
	t *testing.T, r *node.Runtime, home testutil.Home, owner solana.PrivateKey, opener solana.PublicKey,
) solana.PublicKey {
	t.Helper()

	return seedChannelAccount(
		t, r, home, owner, opener, opener, dvpn.PositionModeVoucher, home.ProgramClient.ProgramID,
	)
}

func seedPosition(
	t *testing.T, r *node.Runtime, home testutil.Home, address solana.PublicKey, cumulative uint64,
) {
	t.Helper()

	_, chainNode := node.ChainState(r)
	position := &node.Position{
		AcceptedUnix:   channelOpenedUnix,
		Address:        address.String(),
		Cumulative:     cumulative,
		Kind:           node.KindChannel,
		Mode:           dvpn.PositionModeVoucher,
		NodeOpenedUnix: chainNode.OpenedAt,
		OpenedUnix:     channelOpenedUnix,
		Signature:      make([]byte, solana.SignatureLength),
		Status:         node.PositionStatusOpen,
	}
	require.NoError(t, home.DB.WithContext(t.Context()).Create(position).Error)
}

func seedSession(t *testing.T, home testutil.Home, id, peerID string) *node.Session {
	t.Helper()

	now := time.Now().Unix()
	sess := &node.Session{
		CreatedUnix:     now,
		Credential:      node.CredentialChannel,
		ID:              id,
		LastTrafficUnix: now,
		PeerID:          peerID,
		Status:          node.SessionStatusActive,
		Subject:         id,
		UpdatedUnix:     now,
	}
	require.NoError(t, home.DB.WithContext(t.Context()).Create(sess).Error)

	return sess
}

func voucherRequest(
	t *testing.T,
	home testutil.Home,
	channel solana.PublicKey,
	terms session.Terms,
	cumulative uint64,
) nodeapi.OpenSessionRequest {
	t.Helper()

	signed := voucher.Sign(home.OwnerKey, voucher.ChannelMessage(channel, channelOpenedUnix, cumulative))

	return nodeapi.OpenSessionRequest{
		Credential: nodeapi.Credential{Channel: &channel, Type: nodeapi.CredentialTypeChannel},
		Terms:      &terms,
		Voucher:    &nodeapi.Voucher{Cumulative: signed.CumulativeAmount, Signature: signed.Signature[:]},
	}
}
