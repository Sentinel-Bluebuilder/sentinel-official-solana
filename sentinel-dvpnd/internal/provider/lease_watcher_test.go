//go:build linux

package provider_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/sentinel-official/sentinel-dvpnd/internal/provider"
	"github.com/sentinel-official/sentinel-dvpnd/internal/testutil"
	"github.com/sentinel-official/sentinel-go-sdk/v3/sdktest"
	"github.com/sentinel-official/sentinel-go-sdk/v3/session"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestRuntime_HandleLeaseViolation_ExcludesRevivedNode(t *testing.T) {
	t.Parallel()
	home, r, lease, probe := newLeaseWatchFixture(t)
	require.NoError(t, home.DB.Create(&provider.RosterNode{Node: probe.Node, Plan: planName}).Error)

	provider.HandleLeaseViolation(t.Context(), r, lease, probe, session.ErrUsageMismatch)

	sent := len(home.RPC.Sent())
	for range 2 {
		require.NoError(t, home.DB.Model(&provider.Probe{}).Where("node = ?", probe.Node).Update("live", true).Error)
		require.NoError(t, provider.KeepLeases(t.Context(), r))
	}

	require.Len(t, home.RPC.Sent(), sent)

	var members []provider.RosterNode
	require.NoError(t, home.DB.Where("node = ?", probe.Node).Find(&members).Error)
	require.Empty(t, members)
	require.False(t, provider.WatcherRunning(r, lease.Address))
}

func TestRuntime_HandleLeaseViolation_MarksDeadBeforeCloseTimeout(t *testing.T) {
	t.Parallel()
	home, r, lease, probe := newLeaseWatchFixture(t)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	home.RPC.Simulate = func(_ *solana.Transaction) sdktest.SimulateResult {
		<-ctx.Done()

		return sdktest.SimulateResult{}
	}

	provider.HandleLeaseViolation(ctx, r, lease, probe, errors.New("invalid ledger"))
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)

	var stored provider.Probe
	require.NoError(t, home.DB.First(&stored, "node = ?", probe.Node).Error)
	require.False(t, stored.Live)
	require.Positive(t, stored.DeadSinceUnix)
}

func TestRuntime_StartLeaseWatcher_ExcludesNodeOnInvalidResponse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		setup   func(home testutil.Home, lease provider.Lease)
		trigger func(t *testing.T, home testutil.Home)
	}{
		{
			name: "closed reason outside enum",
			trigger: func(t *testing.T, home testutil.Home) {
				t.Helper()
				require.Eventually(t, func() bool { return home.NodeService.StreamOpenCount() == 1 },
					2*time.Second, 10*time.Millisecond)
				home.NodeService.PublishClosed("weird")
			},
		},
		{
			name: "lease read",
			setup: func(home testutil.Home, lease provider.Lease) {
				home.NodeService.SetLeaseStatus(solana.MustPublicKeyFromBase58(lease.Address), "weird")
			},
		},
		{
			name: "lease stream",
			setup: func(home testutil.Home, _ provider.Lease) {
				home.NodeService.SetLeaseEventsJSON(true)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home, r, lease, probe := newLeaseWatchFixture(t)
			require.NoError(t, home.DB.Create(&provider.RosterNode{Node: probe.Node, Plan: planName}).Error)

			if tc.setup != nil {
				tc.setup(home, lease)
			}

			provider.StartLeaseWatcher(t.Context(), r, lease, probe)

			if tc.trigger != nil {
				tc.trigger(t, home)
			}

			require.Eventually(t, func() bool { return !provider.WatcherRunning(r, lease.Address) },
				2*time.Second, 10*time.Millisecond)
			require.Equal(t, 1, home.NodeService.LeaseReadCount())

			var members []provider.RosterNode
			require.NoError(t, home.DB.Where("node = ?", probe.Node).Find(&members).Error)
			require.Empty(t, members)

			var stored provider.Probe
			require.NoError(t, home.DB.First(&stored, "node = ?", probe.Node).Error)
			require.False(t, stored.Live)
			require.NotEmpty(t, home.RPC.Sent())
			require.True(t, bytes.HasPrefix(
				[]byte(home.RPC.Sent()[0].Message.Instructions[0].Data),
				dvpn.StartCloseLeaseInstructionDiscriminator[:],
			))
		})
	}
}

func TestRuntime_StartLeaseWatcher_PaysBlocks(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"opening", "topup", "expired", "escrow tail"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			home, r, lease, probe := newLeaseWatchFixture(t)
			expected := uint64(60_000)

			address := solana.MustPublicKeyFromBase58(lease.Address)
			if name != "opening" {
				lease.OpenedUnix -= 30
				lease.Cumulative = 60_000
				require.NoError(t, home.DB.Save(&lease).Error)
				home.NodeService.SeedLease(home.DaemonKey, address, lease.Cumulative, 0,
					lease.Escrow, lease.RatePerHour, lease.OpenedUnix)
				home.NodeService.SetLeaseUsage(address, session.Usage{Seconds: 30})

				expected = 90_000

				if name == "expired" {
					lease.OpenedUnix -= 30
					lease.Cumulative = 30_000
					require.NoError(t, home.DB.Save(&lease).Error)
					home.NodeService.SeedLease(home.DaemonKey, address, lease.Cumulative, 0,
						lease.Escrow, lease.RatePerHour, lease.OpenedUnix)
					home.NodeService.SetLeaseUsage(address, session.Usage{Seconds: 60})

					expected = 120_000
				}
			}

			if name == "escrow tail" {
				lease.OpenedUnix -= 10
				lease.Cumulative = 45_000
				lease.Escrow = 50_000
				require.NoError(t, home.DB.Save(&lease).Error)
				home.NodeService.SeedLease(home.DaemonKey, address, lease.Cumulative, 0,
					lease.Escrow, lease.RatePerHour, lease.OpenedUnix)
				home.NodeService.SetLeaseUsage(address, session.Usage{Seconds: 40})

				expected = 50_000
			}

			provider.StartLeaseWatcher(t.Context(), r, lease, probe)
			require.Eventually(
				t, func() bool { return home.NodeService.LeaseVoucherCount() > 0 }, 2*time.Second, 10*time.Millisecond,
			)

			paid := home.NodeService.LeaseVoucher(0).Cumulative
			require.InDelta(t, expected, paid, 1_000)

			if name == "escrow tail" {
				require.Equal(t, lease.Escrow, paid)
			}

			require.Eventually(t, func() bool {
				var stored provider.Lease

				return home.DB.First(&stored, "address = ?", lease.Address).Error == nil && stored.Cumulative == paid
			}, time.Second, 10*time.Millisecond)
		})
	}
}

func TestRuntime_StartLeaseWatcher_PollsWhileStreamSilent(t *testing.T) {
	t.Parallel()
	home, r, lease, probe := newLeaseWatchFixture(t)
	home.NodeService.SetHeartbeatInterval(0)
	provider.StartLeaseWatcher(t.Context(), r, lease, probe)
	require.Eventually(
		t, func() bool { return home.NodeService.LeaseReadCount() >= 3 }, 12*time.Second, 20*time.Millisecond,
	)
	require.True(t, provider.WatcherRunning(r, lease.Address))
	require.GreaterOrEqual(t, home.NodeService.StreamOpenCount(), 2)
	reopened := home.NodeService.ResumeHeartbeats(100 * time.Millisecond)
	require.Eventually(t, func() bool { return home.NodeService.StreamOpenCount() >= reopened },
		12*time.Second, 20*time.Millisecond)

	reads := home.NodeService.LeaseReadCount()

	require.Never(t, func() bool { return home.NodeService.LeaseReadCount() > reads },
		6*time.Second, 20*time.Millisecond)
	provider.StopLeaseWatcher(r, lease.Address)
	require.False(t, provider.WatcherRunning(r, lease.Address))
}

func TestRuntime_StartLeaseWatcher_RecoversContactWithoutKeepLeases(t *testing.T) {
	t.Parallel()
	home, r, lease, probe := newLeaseWatchFixture(t)
	home.NodeService.SetHeartbeatInterval(0)
	home.Loaded.Provider.Lease.NodeTimeoutSeconds = 1

	provider.StartLeaseWatcher(t.Context(), r, lease, probe)
	require.Eventually(t, func() bool { return home.NodeService.LeaseReadCount() >= 2 },
		8*time.Second, 10*time.Millisecond)

	require.True(t, provider.WatcherRunning(r, lease.Address))
	require.Empty(t, home.RPC.Sent())
}

func TestRuntime_StartLeaseWatcher_RefusesInvalidLedger(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{
		"foreign signature", "repricing", "inflated usage", "settled", "escrow mismatch", "unpayable opening",
	} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			home, r, lease, probe := newLeaseWatchFixture(t)
			signer, paid, price, settled := home.DaemonKey, uint64(60_000), lease.RatePerHour, uint64(0)
			escrow := lease.Escrow

			switch scenario {
			case "foreign signature":
				signer = solana.NewWallet().PrivateKey
			case "repricing":
				price++
			case "settled":
				settled = 1
			case "escrow mismatch":
				escrow++
			case "unpayable opening":
				lease.OpenedUnix -= 90
				lease.Escrow = paid
				escrow = paid

				require.NoError(t, home.DB.Save(&lease).Error)
			}

			address := solana.MustPublicKeyFromBase58(lease.Address)
			home.NodeService.SeedLease(signer, address, paid, settled, escrow, price, lease.OpenedUnix)

			switch scenario {
			case "inflated usage":
				home.NodeService.SetLeaseUsage(address, session.Usage{Seconds: 20})
			case "unpayable opening":
				home.NodeService.SetLeaseUsage(address, session.Usage{Seconds: 90})
			}

			provider.StartLeaseWatcher(t.Context(), r, lease, probe)
			require.Eventually(t, func() bool {
				var stored provider.Probe

				return home.DB.First(&stored, "node = ?", probe.Node).Error == nil && !stored.Live && len(home.RPC.Sent()) > 0
			}, 3*time.Second, 10*time.Millisecond)
			require.Zero(t, home.NodeService.LeaseVoucherCount())
			require.True(t, bytes.HasPrefix(
				[]byte(home.RPC.Sent()[0].Message.Instructions[0].Data),
				dvpn.StartCloseLeaseInstructionDiscriminator[:],
			))
		})
	}
}

func TestRuntime_StartLeaseWatcher_SizesBlockAtLedgerPrice(t *testing.T) {
	t.Parallel()
	home, r, lease, probe := newLeaseWatchFixture(t)
	home.NodeService.SeedLease(home.DaemonKey, solana.MustPublicKeyFromBase58(lease.Address),
		0, 0, lease.Escrow, 1, lease.OpenedUnix)

	provider.StartLeaseWatcher(t.Context(), r, lease, probe)
	require.Eventually(
		t, func() bool { return home.NodeService.LeaseVoucherCount() >= 1 }, 2*time.Second, 10*time.Millisecond,
	)
	require.GreaterOrEqual(t, home.NodeService.LeaseVoucher(0).Cumulative, uint64(2))
}

func TestRuntime_StartLeaseWatcher_SkipsInvalidPrepayBlock(t *testing.T) {
	t.Parallel()

	cases := []struct {
		maxPrepaySeconds uint64
		minPrepaySeconds uint64
		name             string
		ratePerHour      uint64
	}{
		{name: "zero block"},
		{maxPrepaySeconds: 60, minPrepaySeconds: 60, name: "maximum below the two-unit floor", ratePerHour: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home, r, lease, probe := newLeaseWatchFixture(t)
			address := solana.MustPublicKeyFromBase58(lease.Address)

			probe.MinLeasePrepaySeconds = tc.minPrepaySeconds
			if tc.maxPrepaySeconds != 0 {
				home.Loaded.Provider.Lease.MaxPrepaySeconds = tc.maxPrepaySeconds
			}

			home.NodeService.SeedLease(home.DaemonKey, address, 0, 0, lease.Escrow, tc.ratePerHour, lease.OpenedUnix)

			provider.StartLeaseWatcher(t.Context(), r, lease, probe)
			require.Eventually(t, func() bool { return !provider.WatcherRunning(r, lease.Address) },
				2*time.Second, 10*time.Millisecond)

			require.Zero(t, home.NodeService.LeaseVoucherCount())
			require.Empty(t, home.RPC.Sent())

			var stored provider.Probe
			require.NoError(t, home.DB.First(&stored, "node = ?", probe.Node).Error)
			require.True(t, stored.Live)
		})
	}
}

func TestRuntime_StartLeaseWatcher_SkipsMinimumAboveMaximum(t *testing.T) {
	t.Parallel()
	home, r, lease, probe := newLeaseWatchFixture(t)
	probe.MinLeasePrepaySeconds = 20_000
	provider.StartLeaseWatcher(t.Context(), r, lease, probe)
	require.Eventually(
		t, func() bool { return !provider.WatcherRunning(r, lease.Address) }, time.Second, 10*time.Millisecond,
	)
	require.Zero(t, home.NodeService.LeaseVoucherCount())
	require.Empty(t, home.RPC.Sent())
}

func TestRuntime_StartLeaseWatcher_StopsAndDeduplicates(t *testing.T) {
	t.Parallel()
	home, r, lease, probe := newLeaseWatchFixture(t)
	provider.StartLeaseWatcher(t.Context(), r, lease, probe)
	provider.StartLeaseWatcher(t.Context(), r, lease, probe)
	require.Eventually(
		t, func() bool { return home.NodeService.StreamOpenCount() == 1 }, 2*time.Second, 10*time.Millisecond,
	)
	home.NodeService.PublishClosed("position closed on chain")
	require.Eventually(
		t, func() bool { return !provider.WatcherRunning(r, lease.Address) }, 2*time.Second, 10*time.Millisecond,
	)
	require.Equal(t, 1, home.NodeService.LeaseVoucherCount())
}

func TestRuntime_StartLeaseWatcher_StopsCleanlyWhenLeaseBecomesInactive(t *testing.T) {
	t.Parallel()
	home, r, lease, probe := newLeaseWatchFixture(t)

	var output bytes.Buffer
	provider.SetLogger(r, zerolog.New(zerolog.SyncWriter(&output)))
	require.NoError(t, home.DB.Callback().Update().After("gorm:update").Register("provider_test:inactive_after_ack",
		func(db *gorm.DB) {
			values, ok := db.Statement.Dest.(map[string]any)
			if !ok || values["cumulative"] == nil {
				return
			}

			require.NoError(t, home.DB.Session(&gorm.Session{NewDB: true}).Model(&provider.Lease{}).
				Where("address = ?", lease.Address).Update("status", provider.StatusClosing).Error)
		}))

	provider.StartLeaseWatcher(t.Context(), r, lease, probe)
	require.Eventually(t, func() bool { return !provider.WatcherRunning(r, lease.Address) },
		2*time.Second, 10*time.Millisecond)

	require.Equal(t, 1, home.NodeService.LeaseVoucherCount())
	require.NotContains(t, output.String(), "paying lease failed")
}

func TestRuntime_StartLeaseWatcher_UsesPinnedPriceAndUpdatedEscrow(t *testing.T) {
	t.Parallel()
	home, r, lease, probe := newLeaseWatchFixture(t)
	address := solana.MustPublicKeyFromBase58(lease.Address)
	lease.Escrow = 15_000
	require.NoError(t, home.DB.Save(&lease).Error)
	home.NodeService.SeedLease(home.DaemonKey, address, 0, 0, lease.Escrow, 1_800_000, lease.OpenedUnix)
	require.NoError(t, home.DB.Callback().Update().After("gorm:update").Register("provider_test:topup_after_ack",
		func(db *gorm.DB) {
			values, ok := db.Statement.Dest.(map[string]any)
			if !ok || values["cumulative"] != uint64(15_000) {
				return
			}

			require.NoError(t, home.DB.Session(&gorm.Session{NewDB: true}).Model(&provider.Lease{}).
				Where("address = ?", lease.Address).Update("escrow", 100_000_000).Error)
			home.NodeService.SeedLease(home.DaemonKey, address, 15_000, 0, 100_000_000, 1_800_000, lease.OpenedUnix)
			home.RPC.SetAccount(home.NodeAddress, sdktest.Account{})
		}))
	provider.StartLeaseWatcher(t.Context(), r, lease, probe)
	require.Eventually(
		t, func() bool { return home.NodeService.LeaseVoucherCount() >= 2 }, 3*time.Second, 10*time.Millisecond,
	)
	require.Equal(t, uint64(15_000), home.NodeService.LeaseVoucher(0).Cumulative)
	require.InDelta(t, 30_000, home.NodeService.LeaseVoucher(1).Cumulative, 1_000)
	require.True(t, provider.WatcherRunning(r, lease.Address))

	var stored provider.Lease

	require.Eventually(t, func() bool {
		return home.DB.First(&stored, "address = ?", lease.Address).Error == nil && stored.Cumulative > 15_000
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, uint64(100_000_000), stored.Escrow)
}

func TestRuntime_SyncLeases_AbsentAccountPreservesConcurrentVoucher(t *testing.T) {
	t.Parallel()
	home, r, lease, _ := newLeaseWatchFixture(t)
	signature := []byte("accepted signature")

	require.NoError(t, home.DB.Callback().Query().After("gorm:query").Register("provider_test:accepted_voucher",
		func(db *gorm.DB) {
			if _, ok := db.Statement.Dest.(*[]provider.Lease); !ok {
				return
			}

			require.NoError(t, home.DB.Session(&gorm.Session{NewDB: true}).Model(&provider.Lease{}).
				Where("address = ?", lease.Address).Updates(map[string]any{
				"cumulative": uint64(90_000), "signature": signature,
			}).Error)
		}))
	require.NoError(t, provider.SyncLeases(t.Context(), r))

	var stored provider.Lease
	require.NoError(t, home.DB.First(&stored, "address = ?", lease.Address).Error)
	require.Equal(t, provider.StatusClosed, stored.Status)
	require.False(t, stored.Healthy)
	require.Equal(t, uint64(90_000), stored.Cumulative)
	require.Equal(t, signature, stored.Signature)
}

func TestRuntime_SyncLeases_RetainsVoucherUnlessRedeemedAdvances(t *testing.T) {
	t.Parallel()
	home, r, lease, _ := newLeaseWatchFixture(t)
	lease.Cumulative = 60_000
	lease.Signature = []byte("saved signature")
	require.NoError(t, home.DB.Save(&lease).Error)
	chainLease := dvpn.Lease{
		Escrow: lease.Escrow + 1, Mode: dvpn.PositionModeVoucher, Node: home.NodeAddress,
		NodeOpenedAt: lease.NodeOpenedUnix, OpenedAt: lease.OpenedUnix, Owner: home.OwnerAddress,
		Provider: providerAddress(t, home),
		Redeemed: 30_000, Status: dvpn.PositionStatusOpen,
	}
	address := solana.MustPublicKeyFromBase58(lease.Address)
	seed := func() {
		home.RPC.SetAccount(address, sdktest.Account{
			Data: sdktest.EncodeAccount(dvpn.LeaseAccountDiscriminator, chainLease), Owner: home.ProgramClient.ProgramID,
		})
	}
	seed()
	require.NoError(t, provider.SyncLeases(t.Context(), r))

	var stored provider.Lease
	require.NoError(t, home.DB.First(&stored, "address = ?", lease.Address).Error)
	require.Equal(t, lease.Cumulative, stored.Cumulative)
	require.Equal(t, lease.Signature, stored.Signature)
	require.Equal(t, chainLease.Escrow, stored.Escrow)
	chainLease.Redeemed = 70_000

	seed()
	require.NoError(t, provider.SyncLeases(t.Context(), r))
	require.NoError(t, home.DB.First(&stored, "address = ?", lease.Address).Error)
	require.Equal(t, chainLease.Redeemed, stored.Cumulative)
	require.Empty(t, stored.Signature)
}
