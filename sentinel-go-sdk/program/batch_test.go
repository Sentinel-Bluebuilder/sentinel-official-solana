package program_test

import (
	"testing"

	"github.com/solana-foundation/solana-go/v2"
	"github.com/stretchr/testify/require"

	"github.com/sentinel-official/sentinel-go-sdk/v3/program"
	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

func TestCapacityFor(t *testing.T) {
	t.Parallel()

	require.Equal(t, 9, program.CapacityFor(program.BatchRedeemVouchers, 1232))
	require.Equal(t, 36, program.CapacityFor(program.BatchRedeemVouchers, 4096))
	require.Equal(t, 36, program.CapacityFor(program.BatchRedeemVouchers, 5000))
	require.Equal(t, 29, program.CapacityFor(program.BatchRedeemStreams, 1232))
	require.Equal(t, 60, program.CapacityFor(program.BatchRedeemStreams, 4096))
	require.Equal(t, 8, program.CapacityFor(program.BatchRedeemPartialVouchers, 1232))
	require.Equal(t, 33, program.CapacityFor(program.BatchRedeemPartialVouchers, 4096))
	require.Zero(t, program.CapacityFor(program.BatchRedeemVouchers, 100))
	require.Zero(t, program.CapacityFor(program.BatchRedeemPartialVouchers, 100))
	require.Equal(t, program.CapacityFund, program.CapacityFor(program.BatchFund, 1232))
	require.Equal(t, program.CapacitySweep, program.CapacityFor(program.BatchSweep, 100))
}

func TestCapacityFor_SerializedRedeemTransactions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		count     int
		kind      program.BatchKind
		name      string
		wantBytes int
	}{
		{count: 9, kind: program.BatchRedeemVouchers, name: "vouchers_at_capacity", wantBytes: 1214},
		{count: 10, kind: program.BatchRedeemVouchers, name: "vouchers_over_capacity", wantBytes: 1319},
		{count: 8, kind: program.BatchRedeemPartialVouchers, name: "partial_vouchers_at_capacity", wantBytes: 1173},
		{count: 9, kind: program.BatchRedeemPartialVouchers, name: "partial_vouchers_over_capacity", wantBytes: 1286},
		{count: 29, kind: program.BatchRedeemStreams, name: "streams_at_capacity", wantBytes: 1222},
		{count: 30, kind: program.BatchRedeemStreams, name: "streams_over_capacity", wantBytes: 1255},
		{count: 60, kind: program.BatchRedeemStreams, name: "streams_at_address_limit", wantBytes: 2245},
	}

	var payer solana.PublicKey

	payer[0] = 250

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			accounts := make([]*solana.AccountMeta, tc.count)
			for i := range accounts {
				var key solana.PublicKey

				key[0] = byte(i + 1)
				key[31] = 1
				accounts[i] = solana.NewAccountMeta(key, true, false)
			}

			var ix solana.Instruction

			var err error

			switch tc.kind {
			case program.BatchRedeemPartialVouchers:
				ix, err = dvpn.BuildRedeemChannelVouchersPartialInstruction(dvpn.ProgramID, dvpn.RedeemChannelVouchersPartialParams{
					Node:              solana.PublicKey{},
					RemainingAccounts: accounts,
					Signer:            payer,
					Vouchers:          make([]dvpn.PartialVoucher, tc.count),
				})
			case program.BatchRedeemVouchers:
				ix, err = dvpn.BuildRedeemChannelVouchersInstruction(dvpn.ProgramID, dvpn.RedeemChannelVouchersParams{
					Node:              solana.PublicKey{},
					RemainingAccounts: accounts,
					Signer:            payer,
					Vouchers:          make([]dvpn.Voucher, tc.count),
				})
			default:
				ix, err = dvpn.BuildRedeemChannelStreamsInstruction(dvpn.ProgramID, dvpn.RedeemChannelStreamsParams{
					Node:              solana.PublicKey{},
					RemainingAccounts: accounts,
					Signer:            payer,
				})
			}

			require.NoError(t, err)

			cfg := solana.TransactionConfig{}.
				WithComputeUnitLimit(200_000).
				WithLoadedAccountsDataSizeLimit(65_536).
				WithPriorityFee(1)
			tx, err := solana.NewTransaction(
				[]solana.Instruction{ix},
				solana.Hash{},
				solana.TransactionPayer(payer),
				solana.TransactionV1Config(cfg),
			)
			require.NoError(t, err)
			raw, err := tx.MarshalBinary()
			require.NoError(t, err)
			require.Len(t, raw, tc.wantBytes)
			require.Equal(t, tc.count <= program.CapacityFor(tc.kind, 1232), len(raw) <= 1232)
		})
	}
}

func TestComputeUnits(t *testing.T) {
	t.Parallel()

	require.EqualValues(t, 8_100+9*17_700, program.ComputeUnits(program.BatchRedeemVouchers, 9))
	require.EqualValues(t, 17_600+50*2_700, program.ComputeUnits(program.BatchFund, 50))
}

func TestSplitBatches(t *testing.T) {
	t.Parallel()

	subjects := make([]solana.PublicKey, 0, 80)

	for i := range 80 {
		var k solana.PublicKey

		k[0] = byte(i % 40)
		k[1] = byte(i / 40)
		subjects = append(subjects, k)
	}

	batches := program.SplitBatches(program.BatchRedeemVouchers, subjects)
	require.Len(t, batches, 9)
	require.Len(t, batches[0], 9)
	require.Len(t, batches[8], 8)
	require.Len(t, program.SplitBatchesFor(program.BatchRedeemVouchers, 4096, subjects), 3)

	for _, batch := range batches {
		require.NoError(t, program.ValidateBatch(program.BatchRedeemVouchers, batch))
	}

	require.ErrorIs(t, program.ValidateBatch(program.BatchRedeemVouchers, subjects[:10]), program.ErrBatchTooLarge)

	unsorted := []solana.PublicKey{subjects[1], subjects[0]}
	require.ErrorIs(t, program.ValidateBatch(program.BatchSweep, unsorted), dvpn.ErrBatchNotSorted)
}

func TestSplitBatches_UnknownKind(t *testing.T) {
	t.Parallel()

	var k solana.PublicKey

	subjects := []solana.PublicKey{k}

	require.Nil(t, program.SplitBatches(program.BatchKind(99), subjects))
	require.Nil(t, program.SplitBatches(program.BatchSweep, nil))
	require.Nil(t, program.SplitBatchesFor(program.BatchRedeemVouchers, 100, subjects))
}
