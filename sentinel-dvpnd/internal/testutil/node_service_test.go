package testutil_test

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	"github.com/sentinel-official/sentinel-go-sdk/v3/api"
	"github.com/sentinel-official/sentinel-go-sdk/v3/nodeapi"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
	"github.com/sentinel-official/sentinel-go-sdk/v3/voucher"
	"github.com/sentinel-official/sentinel-programs/clients/go/core"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestFakeNodeService_GetChannel_PerChannelState(t *testing.T) {
	t.Parallel()

	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	other, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	fake := testutil.NewFakeNodeService(signer, uuid.NewString())
	fake.SeedLedger(signer, 10_000, 30)

	read := func(address solana.PublicKey) nodeapi.Position {
		response, readErr := fake.GetChannel(t.Context(), nodeapi.GetChannelRequestObject{Channel: address})
		require.NoError(t, readErr)

		return nodeapi.Position(response.(nodeapi.GetChannel200JSONResponse))
	}

	first := read(signer.PublicKey())
	require.Equal(t, uint64(10_000), first.Ledger.CumulativeBaseUnits)

	second := read(other.PublicKey())
	require.Zero(t, second.Ledger.CumulativeBaseUnits)
	require.Nil(t, second.Ledger.Signature)
	require.Zero(t, second.Consumption.SettledBaseUnits)
	require.Equal(t, uint64(1_000_000_000), second.Ledger.EscrowBaseUnits)

	again := read(signer.PublicKey())
	require.Equal(t, uint64(10_000), again.Ledger.CumulativeBaseUnits)
	require.Equal(t, uint64(30), again.Consumption.SettledBaseUnits)
}

func TestFakeNodeService_GetChannel_SignedConsumption(t *testing.T) {
	t.Parallel()

	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	address := signer.PublicKey()

	fake := testutil.NewFakeNodeService(signer, uuid.NewString())
	fake.SeedLedger(signer, 10_000, 30)
	fake.SetUsage(session.Usage{RxBytes: 1})
	fake.SetEscrow(50_000)
	fake.SetRedeemed(20)

	response, err := fake.GetChannel(t.Context(), nodeapi.GetChannelRequestObject{Channel: address})
	require.NoError(t, err)

	position := response.(nodeapi.GetChannel200JSONResponse)
	require.Equal(t, uint64(31), position.Consumption.OwedBaseUnits)
	require.Equal(t, uint64(30), position.Consumption.SettledBaseUnits)
	require.Equal(t, uint64(50_000), position.Ledger.EscrowBaseUnits)
	require.Equal(t, uint64(20), position.Ledger.RedeemedBaseUnits)
	require.Equal(t, uint64(5_000), position.Ledger.Price)

	signed := voucher.Sign(signer, voucher.ChannelMessage(address, 0, 10_000))
	require.Equal(t, signed.Signature[:], position.Ledger.Signature)

	fake.SetSettled(math.MaxUint64)
	response, err = fake.GetChannel(t.Context(), nodeapi.GetChannelRequestObject{Channel: address})
	require.NoError(t, err)
	require.Equal(t, uint64(math.MaxUint64), response.(nodeapi.GetChannel200JSONResponse).Consumption.OwedBaseUnits)
}

func TestFakeNodeService_GetLease_IndependentLedgers(t *testing.T) {
	t.Parallel()

	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	address := signer.PublicKey()

	fake := testutil.NewFakeNodeService(signer, uuid.NewString())
	fake.SeedLedger(signer, 99, 10)
	fake.SeedLease(signer, address, 2_000, 100, 10_000, 3_600, 42)
	fake.SetLeaseUsage(address, session.Usage{Seconds: 5})

	response, err := fake.GetLease(t.Context(), nodeapi.GetLeaseRequestObject{Lease: address})
	require.NoError(t, err)

	position := response.(nodeapi.GetLease200JSONResponse)
	require.Equal(t, uint64(105), position.Consumption.OwedBaseUnits)
	require.Equal(t, int64(42), position.Ledger.OpenedAt)

	signed := voucher.Sign(signer, voucher.LeaseMessage(address, 42, 2_000))
	require.Equal(t, signed.Signature[:], position.Ledger.Signature)

	for _, cumulative := range []uint64{3_000, 2_500} {
		_, err = fake.SendLeaseVoucher(t.Context(), nodeapi.SendLeaseVoucherRequestObject{
			Body:  &nodeapi.SendVoucherRequest{Voucher: nodeapi.Voucher{Cumulative: cumulative}},
			Lease: address,
		})
		require.NoError(t, err)
	}

	response, err = fake.GetLease(t.Context(), nodeapi.GetLeaseRequestObject{Lease: address})
	require.NoError(t, err)
	require.Equal(t, uint64(3_000), response.(nodeapi.GetLease200JSONResponse).Ledger.CumulativeBaseUnits)
	channel, err := fake.GetChannel(t.Context(), nodeapi.GetChannelRequestObject{Channel: address})
	require.NoError(t, err)
	require.Equal(t, uint64(99), channel.(nodeapi.GetChannel200JSONResponse).Ledger.CumulativeBaseUnits)
	require.Equal(t, 2, fake.LeaseVoucherCount())
}

func TestFakeNodeService_GetSessionEvents_BroadcastAndClose(t *testing.T) {
	t.Parallel()

	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	fake := testutil.NewFakeNodeService(signer, uuid.NewString())
	fake.SetHeartbeatInterval(0)
	fake.SetUsage(session.Usage{RxBytes: 1})

	address := signer.PublicKey()

	fake.SeedLease(signer, address, 100, 2, 1_000, 3_600, 0)
	fake.SetLeaseUsage(address, session.Usage{Seconds: 5})

	readers := make([]io.Reader, 0, 3)

	for range 2 {
		response, streamErr := fake.GetSessionEvents(t.Context(), nodeapi.GetSessionEventsRequestObject{})
		require.NoError(t, streamErr)

		readers = append(readers, response.(nodeapi.GetSessionEvents200TextEventStreamResponse).Body)
	}

	response, err := fake.GetLeaseEvents(t.Context(), nodeapi.GetLeaseEventsRequestObject{Lease: address})
	require.NoError(t, err)

	readers = append(readers, response.(nodeapi.GetLeaseEvents200TextEventStreamResponse).Body)

	require.Equal(t, 3, fake.StreamOpenCount())

	for range 100 {
		fake.PublishVoucherRequest()
	}

	fake.PublishClosed(nodeapi.ReasonIdle)

	for index, reader := range readers {
		decoder := api.NewEventDecoder(reader)
		for range 100 {
			event, decodeErr := decoder.Next()
			require.NoError(t, decodeErr)
			require.Equal(t, nodeapi.EventVoucherRequest, event.Name)

			var snapshot nodeapi.VoucherRequestEvent
			require.NoError(t, json.Unmarshal(event.Data, &snapshot))

			owed := uint64(1)
			if index == 2 {
				owed = 7
			}

			require.Equal(t, owed, snapshot.Consumption.OwedBaseUnits)
		}

		event, decodeErr := decoder.Next()
		require.NoError(t, decodeErr)
		require.Equal(t, nodeapi.EventClosed, event.Name)

		var closed nodeapi.ClosedEvent
		require.NoError(t, json.Unmarshal(event.Data, &closed))
		require.Equal(t, nodeapi.ClosedReasonIdle, closed.Reason)

		_, decodeErr = decoder.Next()
		require.ErrorIs(t, decodeErr, io.EOF)
	}
}

func TestFakeNodeService_GetSessionEvents_HeartbeatAndCancellation(t *testing.T) {
	t.Parallel()

	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	fake := testutil.NewFakeNodeService(signer, uuid.NewString())
	fake.SetHeartbeatInterval(time.Millisecond)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	response, err := fake.GetSessionEvents(ctx, nodeapi.GetSessionEventsRequestObject{})
	require.NoError(t, err)

	decoder := api.NewEventDecoder(response.(nodeapi.GetSessionEvents200TextEventStreamResponse).Body)
	event, err := decoder.Next()
	require.NoError(t, err)
	require.True(t, event.IsComment)
	cancel()

	_, err = io.ReadAll(response.(nodeapi.GetSessionEvents200TextEventStreamResponse).Body)
	require.ErrorIs(t, err, context.Canceled)
}

func TestFakeNodeService_OpenedRequest_CredentialSnapshot(t *testing.T) {
	t.Parallel()

	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	address := signer.PublicKey()
	ticket, ticketRequest := "ticket", "ticket-request"
	fake := testutil.NewFakeNodeService(signer, uuid.NewString())
	_, err = fake.OpenSession(t.Context(), nodeapi.OpenSessionRequestObject{Body: &nodeapi.OpenSessionRequest{
		Credential: nodeapi.Credential{
			Channel:       &address,
			Ticket:        &ticket,
			TicketRequest: &ticketRequest,
			Type:          nodeapi.CredentialTypeChannel,
		},
	}})
	require.NoError(t, err)

	address, ticket, ticketRequest = solana.PublicKey{}, "changed", "changed-request"
	snapshot := fake.OpenedRequest(0)
	require.Equal(t, signer.PublicKey(), *snapshot.Credential.Channel)
	require.Equal(t, "ticket", *snapshot.Credential.Ticket)
	require.Equal(t, "ticket-request", *snapshot.Credential.TicketRequest)

	*snapshot.Credential.Channel = address
	*snapshot.Credential.Ticket = ticket
	*snapshot.Credential.TicketRequest = ticketRequest
	snapshot = fake.OpenedRequest(0)
	require.Equal(t, signer.PublicKey(), *snapshot.Credential.Channel)
	require.Equal(t, "ticket", *snapshot.Credential.Ticket)
	require.Equal(t, "ticket-request", *snapshot.Credential.TicketRequest)
}

func TestFakeNodeService_OpenSession_RejectionAndRetry(t *testing.T) {
	t.Parallel()

	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	fake := testutil.NewFakeNodeService(signer, uuid.NewString())
	fake.SetOpenRejections(1)
	fake.SetTicket("ticket")

	address := signer.PublicKey()
	request := nodeapi.OpenSessionRequestObject{Body: &nodeapi.OpenSessionRequest{
		Credential: nodeapi.Credential{Channel: &address, Type: nodeapi.CredentialTypeChannel},
		Terms:      &session.Terms{Basis: session.BasisPerHour, Price: 200_000},
		Voucher:    &nodeapi.Voucher{Cumulative: 20_000},
	}}

	response, err := fake.OpenSession(t.Context(), request)
	require.NoError(t, err)

	rejected := response.(nodeapi.OpenSessiondefaultJSONResponse)
	require.Equal(t, api.CodePrepayBelowMinimum, rejected.Body.Error.Code)

	response, err = fake.OpenSession(t.Context(), request)
	require.NoError(t, err)

	opened := response.(nodeapi.OpenSession200JSONResponse)
	require.Equal(t, fake.SessionIDs()[1], opened.SessionID)
	require.Equal(t, "ticket", *opened.Ticket)
	require.Equal(t, session.BasisPerHour, opened.Ledger.Basis)
	require.Equal(t, uint64(20_000), opened.Ledger.CumulativeBaseUnits)
	require.Equal(t, 2, fake.OpenedCount())

	fake.SetGetSessionErr(api.ErrSessionNotFound, 1)
	responseSession, err := fake.GetSession(t.Context(), nodeapi.GetSessionRequestObject{})
	require.NoError(t, err)
	require.IsType(t, nodeapi.GetSessiondefaultJSONResponse{}, responseSession)
	responseSession, err = fake.GetSession(t.Context(), nodeapi.GetSessionRequestObject{})
	require.NoError(t, err)
	require.IsType(t, nodeapi.GetSession200JSONResponse{}, responseSession)
}

func TestWithProgramClient(t *testing.T) {
	t.Parallel()

	signer, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	programID := solana.PublicKey{1}
	channelAddress := solana.PublicKey{2}
	leaseAddress := solana.PublicKey{3}

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.SetAccount(channelAddress, sdktest.Account{
		Data: sdktest.EncodeAccount(
			dvpn.ChannelAccountDiscriminator, dvpn.Channel{Escrow: 7_000, OpenedAt: 42, Redeemed: 300},
		),
		Owner: programID,
	})
	fakeRPC.SetAccount(leaseAddress, sdktest.Account{
		Data: sdktest.EncodeAccount(
			dvpn.LeaseAccountDiscriminator, dvpn.Lease{Escrow: 9_000, OpenedAt: 43, Redeemed: 100},
		),
		Owner: programID,
	})

	programClient := program.NewClient(fakeRPC.NewClient(), programID, solana.PublicKey{4}, rpc.CommitmentConfirmed)
	fake := testutil.NewFakeNodeService(signer, uuid.NewString(), testutil.WithProgramClient(programClient))

	channel, err := fake.GetChannel(t.Context(), nodeapi.GetChannelRequestObject{Channel: channelAddress})
	require.NoError(t, err)

	ledger := channel.(nodeapi.GetChannel200JSONResponse).Ledger
	require.Equal(t, uint64(7_000), ledger.EscrowBaseUnits)
	require.Equal(t, uint64(300), ledger.RedeemedBaseUnits)
	require.Equal(t, int64(42), ledger.OpenedAt)

	lease, err := fake.GetLease(t.Context(), nodeapi.GetLeaseRequestObject{Lease: leaseAddress})
	require.NoError(t, err)

	ledger = lease.(nodeapi.GetLease200JSONResponse).Ledger
	require.Equal(t, uint64(9_000), ledger.EscrowBaseUnits)
	require.Equal(t, uint64(100), ledger.RedeemedBaseUnits)
	require.Equal(t, int64(43), ledger.OpenedAt)

	request := func(address solana.PublicKey) nodeapi.OpenSessionRequestObject {
		return nodeapi.OpenSessionRequestObject{Body: &nodeapi.OpenSessionRequest{
			Credential: nodeapi.Credential{Channel: &address, Type: nodeapi.CredentialTypeChannel},
		}}
	}

	opened, err := fake.OpenSession(t.Context(), request(channelAddress))
	require.NoError(t, err)
	require.Equal(t, uint64(7_000), opened.(nodeapi.OpenSession200JSONResponse).Ledger.EscrowBaseUnits)

	_, err = fake.OpenSession(t.Context(), request(solana.PublicKey{5}))
	require.ErrorIs(t, err, core.ErrAccountNotFound)

	_, err = fake.GetChannel(t.Context(), nodeapi.GetChannelRequestObject{Channel: channelAddress})
	require.NoError(t, err)
}
