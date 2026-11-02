package client_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/solana-foundation/solana-go/v2/rpc"
	"github.com/solana-foundation/solana-go/v2/rpc/jsonrpc"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-dvpnd/internal/client"
	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

type cancelLookupTransport struct {
	base    http.RoundTripper
	cancel  context.CancelFunc
	lookups atomic.Int32
}

func (t *cancelLookupTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("reading RPC request: %w", err)
	}

	lookup := bytes.Contains(body, []byte(`"method":"getAccountInfo"`))
	if lookup {
		t.lookups.Add(1)
	}

	if t.cancel != nil && lookup {
		t.cancel()

		return nil, fmt.Errorf("canceling channel lookup: %w", context.Canceled)
	}

	req.Body = io.NopCloser(bytes.NewReader(body))

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("sending RPC request: %w", err)
	}

	return resp, nil
}

type heldLookupTransport struct {
	base    http.RoundTripper
	once    sync.Once
	release chan struct{}
	seen    chan struct{}
}

func (t *heldLookupTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("reading RPC request: %w", err)
	}

	req.Body = io.NopCloser(bytes.NewReader(body))

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("sending RPC request: %w", err)
	}

	if bytes.Contains(body, []byte(`"method":"getAccountInfo"`)) {
		t.once.Do(func() { close(t.seen) })
		<-t.release
	}

	return resp, nil
}

type lostAckTransport struct {
	base http.RoundTripper
	once sync.Once
	sent chan struct{}
}

func (t *lostAckTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("reading RPC request: %w", err)
	}

	req.Body = io.NopCloser(bytes.NewReader(body))

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, fmt.Errorf("sending RPC request: %w", err)
	}

	if !bytes.Contains(body, []byte(`"method":"sendTransaction"`)) {
		return resp, nil
	}

	t.once.Do(func() { close(t.sent) })
	<-req.Context().Done()

	_ = resp.Body.Close()

	return nil, fmt.Errorf("losing RPC response: %w", req.Context().Err())
}

func TestRuntime_CloseChannels_SyncsChainStatus(t *testing.T) {
	t.Parallel()

	now := time.Now().Unix()

	tests := []struct {
		absent       int
		closeStarted []int64
		name         string
		sent         int
		want         []string
	}{
		{
			absent:       1,
			closeStarted: []int64{now - 400, 0, now, now - 400},
			name:         "absent account",
			sent:         2,
			want: []string{
				client.ChannelStatusClosed, client.ChannelStatusClosed, client.ChannelStatusClosing,
				client.ChannelStatusClosed,
			},
		},
		{
			absent:       -1,
			closeStarted: []int64{now - 400, now, now - 400, now},
			name:         "batch",
			sent:         2,
			want: []string{
				client.ChannelStatusClosed, client.ChannelStatusClosing, client.ChannelStatusClosed,
				client.ChannelStatusClosing,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fakeRPC := sdktest.NewFakeRPC(t)
			home := testutil.NewClientHome(t, fakeRPC)
			require.NoError(t, fakeRPC.SetConfig(home.ProgramClient.ProgramID, dvpn.Config{
				ChannelCloseGraceSeconds: 300,
			}))
			rt := newRuntime(t, home, home.ProgramClient)
			owner := solana.PublicKey{99}
			grant, _, err := dvpn.FindGrantAddress(
				home.ProgramClient.ProgramID, owner, home.DaemonKey.PublicKey(), solana.PublicKey{},
			)
			require.NoError(t, err)
			fakeRPC.SetAccount(grant, sdktest.Account{
				Data:  []byte("invalid grant"),
				Owner: home.ProgramClient.ProgramID,
			})

			statuses := []string{
				client.ChannelStatusOpen, client.ChannelStatusOpen, client.ChannelStatusOpen,
				client.ChannelStatusClosing,
			}

			require.NoError(t, home.DB.WithContext(t.Context()).Create(&client.Channel{
				Address: "invalid!",
				Status:  client.ChannelStatusOpen,
			}).Error)

			for i, status := range statuses {
				address := solana.PublicKey{byte(i + 1)}
				require.NoError(t, home.DB.WithContext(t.Context()).Create(&client.Channel{
					Address: address.String(),
					Status:  status,
				}).Error)

				if i != tt.absent {
					fakeRPC.SetAccount(address, sdktest.Account{
						Data: sdktest.EncodeAccount(dvpn.ChannelAccountDiscriminator, dvpn.Channel{
							CloseStartedAt: tt.closeStarted[i],
							Mode:           dvpn.PositionModeVoucher,
							Node:           home.NodeAddress,
							Owner:          owner,
							RentRecipient:  home.DaemonKey.PublicKey(),
							Status:         dvpn.PositionStatusClosing,
						}),
						Owner: home.ProgramClient.ProgramID,
					})
				}
			}

			require.NoError(t, client.CloseChannels(t.Context(), rt))

			for i := range statuses {
				var ch client.Channel

				address := solana.PublicKey{byte(i + 1)}
				require.NoError(t, home.DB.WithContext(t.Context()).First(&ch, "address = ?", address.String()).Error)
				require.Equal(t, tt.want[i], ch.Status, "channel %d", i)
			}

			var invalid client.Channel
			require.NoError(t, home.DB.WithContext(t.Context()).First(&invalid, "address = ?", "invalid!").Error)
			require.Equal(t, client.ChannelStatusOpen, invalid.Status)

			require.Len(t, fakeRPC.Sent(), tt.sent)
		})
	}
}

func TestRuntime_ResolvePendingChannel_DropsExpiredAbsentRow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		age   time.Duration
		keep  bool
		name  string
		sweep bool
	}{
		{age: 10 * time.Minute, keep: false, name: "expired", sweep: true},
		{age: 10 * time.Minute, keep: false, name: "expired without sweep", sweep: false},
		{age: time.Minute, keep: true, name: "young", sweep: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rt, home, _ := newWorkingRuntime(t)

			key, err := solana.NewRandomPrivateKey()
			require.NoError(t, err)

			address := key.PublicKey()
			require.NoError(t, home.DB.WithContext(t.Context()).Create(&client.Channel{
				Address:     address.String(),
				NodeAddress: home.NodeAddress.String(),
				Status:      client.ChannelStatusPending,
				UpdatedUnix: time.Now().Add(-tt.age).Unix(),
			}).Error)

			if tt.sweep {
				require.NoError(t, rt.Start(t.Context()))

				var count int64
				require.NoError(t, home.DB.WithContext(t.Context()).
					Model(&client.Channel{}).Where("address = ?", address.String()).Count(&count).Error)
				require.Equal(t, tt.keep, count == 1)
			}

			_, err = client.StartSession(t.Context(), rt, newChannelRequest(home.NodeAddress.String()))
			require.Equal(t, !tt.keep, err == nil)
		})
	}
}

func TestRuntime_Start_AdoptsPendingChannel(t *testing.T) {
	t.Parallel()

	rt, home, _ := newWorkingRuntime(t)
	sess, err := client.StartSession(t.Context(), rt, newChannelRequest(home.NodeAddress.String()))
	require.NoError(t, err)

	var ch client.Channel
	require.NoError(t, home.DB.WithContext(t.Context()).First(&ch, "address = ?", sess.ChannelAddress).Error)
	ch.Status = client.ChannelStatusPending
	require.NoError(t, home.DB.WithContext(t.Context()).Save(&ch).Error)

	require.NoError(t, rt.Start(t.Context()))
	require.NoError(t, home.DB.WithContext(t.Context()).First(&ch, "address = ?", sess.ChannelAddress).Error)
	require.Equal(t, client.ChannelStatusOpen, ch.Status)
}

func TestRuntime_Start_MultiplePendingRowsDoNotDelayStartup(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	home := testutil.NewClientHome(t, fakeRPC)
	transport := &cancelLookupTransport{base: http.DefaultTransport}
	httpClient := &http.Client{Transport: transport}
	rpcClient := jsonrpc.NewClientWithOpts(fakeRPC.Server.URL, &jsonrpc.RPCClientOpts{HTTPClient: httpClient})
	custom := rpc.NewWithCustomRPCClient(rpcClient)
	programClient := program.NewClient(
		custom, home.ProgramClient.ProgramID, home.ProgramClient.Mint, home.ProgramClient.Commitment,
	)
	rt := newRuntime(t, home, programClient)
	client.SetPendingTiming(rt, 10*time.Millisecond, 200*time.Millisecond)

	for range 2 {
		key, err := solana.NewRandomPrivateKey()
		require.NoError(t, err)

		ch := client.Channel{
			Address:     key.PublicKey().String(),
			NodeAddress: home.NodeAddress.String(),
			Status:      client.ChannelStatusPending,
		}
		require.NoError(t, home.DB.WithContext(t.Context()).Save(&ch).Error)
	}

	require.NoError(t, rt.Start(t.Context()))
	require.Equal(t, int32(2), transport.lookups.Load())

	ctx, cancel := context.WithCancel(t.Context())
	transport.cancel = cancel

	require.ErrorIs(t, rt.Start(ctx), context.Canceled)
}

func TestRuntime_Start_PendingChannelSurvivesAbsenceAndWorkerAdoptsLanding(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	home := testutil.NewClientHome(t, fakeRPC)
	rt := newRuntime(t, home, home.ProgramClient)
	client.SetPendingTiming(rt, 10*time.Millisecond, 50*time.Millisecond)

	key, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	address := key.PublicKey()
	ch := client.Channel{
		Address:     address.String(),
		NodeAddress: home.NodeAddress.String(),
		Status:      client.ChannelStatusPending,
	}
	require.NoError(t, home.DB.WithContext(t.Context()).Save(&ch).Error)

	require.NoError(t, rt.Start(t.Context()))
	require.NoError(t, home.DB.WithContext(t.Context()).First(&ch, "address = ?", address.String()).Error)
	require.Equal(t, client.ChannelStatusPending, ch.Status)

	chainChannel := dvpn.Channel{
		Escrow:        5000,
		Mode:          dvpn.PositionModeVoucher,
		Node:          home.NodeAddress,
		Owner:         home.DaemonKey.PublicKey(),
		VoucherSigner: home.DaemonKey.PublicKey(),
	}
	fakeRPC.SetAccount(address, sdktest.Account{
		Data:  sdktest.EncodeAccount(dvpn.ChannelAccountDiscriminator, chainChannel),
		Owner: home.ProgramClient.ProgramID,
	})
	require.NoError(t, client.CloseChannels(t.Context(), rt))
	require.NoError(t, home.DB.WithContext(t.Context()).First(&ch, "address = ?", address.String()).Error)
	require.Equal(t, client.ChannelStatusOpen, ch.Status)
	require.Equal(t, uint64(5000), ch.Escrow)
}

func TestRuntime_Start_PendingSnapshotPreservesNewerRow(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	home := testutil.NewClientHome(t, fakeRPC)
	transport := &heldLookupTransport{
		base:    http.DefaultTransport,
		release: make(chan struct{}),
		seen:    make(chan struct{}),
	}
	httpClient := &http.Client{Transport: transport}
	rpcClient := jsonrpc.NewClientWithOpts(fakeRPC.Server.URL, &jsonrpc.RPCClientOpts{HTTPClient: httpClient})
	custom := rpc.NewWithCustomRPCClient(rpcClient)
	programClient := program.NewClient(
		custom, home.ProgramClient.ProgramID, home.ProgramClient.Mint, home.ProgramClient.Commitment,
	)
	rt := newRuntime(t, home, programClient)

	key, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	address := key.PublicKey()
	ch := client.Channel{
		Address:     address.String(),
		NodeAddress: home.NodeAddress.String(),
		Status:      client.ChannelStatusPending,
	}
	require.NoError(t, home.DB.WithContext(t.Context()).Save(&ch).Error)
	chainChannel := dvpn.Channel{
		Mode:          dvpn.PositionModeVoucher,
		Node:          home.NodeAddress,
		Owner:         home.DaemonKey.PublicKey(),
		VoucherSigner: home.DaemonKey.PublicKey(),
	}
	fakeRPC.SetAccount(address, sdktest.Account{
		Data:  sdktest.EncodeAccount(dvpn.ChannelAccountDiscriminator, chainChannel),
		Owner: home.ProgramClient.ProgramID,
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	started := make(chan error, 1)
	go func() { started <- rt.Start(ctx) }()

	select {
	case <-transport.seen:
	case <-ctx.Done():
		require.FailNow(t, "pending lookup did not start")
	}

	ch.Cumulative = 777
	ch.Status = client.ChannelStatusClosing
	require.NoError(t, home.DB.WithContext(t.Context()).Save(&ch).Error)
	close(transport.release)
	require.NoError(t, awaitErr(t, ctx, started))

	require.NoError(t, home.DB.WithContext(t.Context()).First(&ch, "address = ?", address.String()).Error)
	require.Equal(t, uint64(777), ch.Cumulative)
	require.Equal(t, client.ChannelStatusClosing, ch.Status)
}

func TestRuntime_StartSession_FailedLandingAllowsNewChannelRetry(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.LandedErr = map[string]any{"InstructionError": []any{0, map[string]any{"Custom": 2006}}}
	home := testutil.NewClientHome(t, fakeRPC)
	rt := newRuntime(t, home, home.ProgramClient)
	client.SetPendingTiming(rt, 10*time.Millisecond, 50*time.Millisecond)

	_, err := client.StartSession(t.Context(), rt, newChannelRequest(home.NodeAddress.String()))
	require.Error(t, err)
	require.Len(t, fakeRPC.Sent(), 1)

	var count int64
	require.NoError(t, home.DB.Model(&client.Channel{}).Count(&count).Error)
	require.Zero(t, count)

	_, err = client.StartSession(t.Context(), rt, newChannelRequest(home.NodeAddress.String()))
	require.Error(t, err)
	require.Len(t, fakeRPC.Sent(), 2)
}

func TestRuntime_StartSession_OpensChannelWithPaymasterAsRentPayer(t *testing.T) {
	t.Parallel()

	rt, home, _ := newWorkingRuntime(t)
	paymasterKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)

	home.Sender.Paymaster = sdktest.FakePaymaster{Key: paymasterKey}

	_, err = client.StartSession(t.Context(), rt, newChannelRequest(home.NodeAddress.String()))
	require.NoError(t, err)
	require.Len(t, home.RPC.Sent(), 1)

	openTx := home.RPC.Sent()[0]
	require.Equal(t, paymasterKey.PublicKey(), openTx.Message.AccountKeys[0])

	metas, err := openTx.Message.Instructions[0].ResolveInstructionAccounts(&openTx.Message)
	require.NoError(t, err)
	require.Equal(t, home.DaemonKey.PublicKey(), metas[0].PublicKey)
	require.Equal(t, paymasterKey.PublicKey(), metas[11].PublicKey)
	require.Equal(
		t,
		[]solana.PublicKey{home.DaemonKey.PublicKey(), home.DaemonKey.PublicKey()},
		testutil.ParseOpenRentRecipients(t, openTx.Message.Instructions[0].Data),
	)
}

func TestRuntime_StartSession_PreparationFailureClearsPendingChannel(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.Simulate = func(*solana.Transaction) sdktest.SimulateResult {
		return sdktest.SimulateResult{Err: "simulation rejected"}
	}
	home := testutil.NewClientHome(t, fakeRPC)
	rt := newRuntime(t, home, home.ProgramClient)
	client.SetPendingTiming(rt, 10*time.Millisecond, 50*time.Millisecond)

	_, err := client.StartSession(t.Context(), rt, newChannelRequest(home.NodeAddress.String()))
	require.Error(t, err)
	require.Empty(t, fakeRPC.Sent())

	var channels int64
	require.NoError(t, home.DB.WithContext(t.Context()).Model(&client.Channel{}).Count(&channels).Error)
	require.Zero(t, channels)
}

func TestRuntime_StartSession_RetrySurfacesPendingAddress(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	fakeRPC.Unconfirmed = true
	home := testutil.NewClientHome(t, fakeRPC)
	home.Sender.Config.ConfirmTimeout = 30 * time.Millisecond
	rt := newRuntime(t, home, home.ProgramClient)
	client.SetPendingTiming(rt, 10*time.Millisecond, 50*time.Millisecond)

	req := newChannelRequest(home.NodeAddress.String())

	_, err := client.StartSession(t.Context(), rt, req)
	require.Error(t, err)

	var ch client.Channel
	require.NoError(t, home.DB.WithContext(t.Context()).First(&ch).Error)
	require.Equal(t, client.ChannelStatusPending, ch.Status)

	_, err = client.StartSession(t.Context(), rt, newChannelRequest(home.NodeAddress.String()))
	require.ErrorContains(t, err, ch.Address)
	require.Len(t, fakeRPC.Sent(), 1)
}

func TestRuntime_StartSession_StopDuringOpenChannelKeepsChannel(t *testing.T) {
	t.Parallel()

	fakeRPC := sdktest.NewFakeRPC(t)
	home := testutil.NewClientHome(t, fakeRPC)
	transport := &lostAckTransport{base: http.DefaultTransport, sent: make(chan struct{})}
	httpClient := &http.Client{Transport: transport}
	rpcClient := jsonrpc.NewClientWithOpts(fakeRPC.Server.URL, &jsonrpc.RPCClientOpts{HTTPClient: httpClient})
	sender := *home.Sender
	sender.RPC = rpc.NewWithCustomRPCClient(rpcClient)
	home.Sender = &sender
	rt := newRuntime(t, home, installOpenChannelReactor(t, fakeRPC, home))

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	started := make(chan error, 1)

	go func() {
		_, err := client.StartSession(ctx, rt, newChannelRequest(home.NodeAddress.String()))
		started <- err
	}()

	select {
	case <-transport.sent:
	case <-ctx.Done():
		require.FailNow(t, "open transaction was not sent")
	}

	require.NoError(t, client.StopSession(ctx, rt))
	require.ErrorIs(t, awaitErr(t, ctx, started), client.ErrStoppedDuringConnect)

	require.Eventually(t, func() bool {
		var channels []client.Channel
		if home.DB.WithContext(t.Context()).Find(&channels).Error != nil || len(channels) != 1 {
			return false
		}

		return channels[0].Address != "" && channels[0].Status == client.ChannelStatusOpen
	}, 5*time.Second, 20*time.Millisecond)
}
