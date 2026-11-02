package program

import (
	"bytes"
	"errors"
	"fmt"
	"slices"

	"github.com/solana-foundation/solana-go/v2"

	"github.com/sentinel-official/sentinel-programs/clients/go/dvpn"
)

const (
	BatchRedeemVouchers BatchKind = iota + 1
	BatchRedeemStreams
	BatchFund
	BatchSweep
)

const (
	CapacityFund               = 50
	CapacitySweep              = 55
	DefaultMaxTransactionBytes = 1232
)

const (
	FundBaseComputeUnits              uint32 = 17_600
	FundPerItemComputeUnits           uint32 = 2_700
	RedeemStreamsBaseComputeUnits     uint32 = 8_200
	RedeemStreamsPerItemComputeUnits  uint32 = 3_000
	RedeemVouchersBaseComputeUnits    uint32 = 8_100
	RedeemVouchersPerItemComputeUnits uint32 = 17_700
	SweepBaseComputeUnits             uint32 = 12_500
	SweepPerItemComputeUnits          uint32 = 2_300
)

const (
	maxBatchAddresses          = 60
	redeemStreamsBaseBytes     = 265
	redeemStreamsPerItemBytes  = 33
	redeemVouchersBaseBytes    = 269
	redeemVouchersPerItemBytes = 105
)

var ErrBatchTooLarge = errors.New("batch too large")

type BatchKind uint8

func Capacity(kind BatchKind) int {
	return CapacityFor(kind, DefaultMaxTransactionBytes)
}

func CapacityFor(kind BatchKind, maxBytes int) int {
	maxBytes = min(maxBytes, solana.MaxTransactionSizeV1)

	switch kind {
	case BatchRedeemVouchers:
		if maxBytes < redeemVouchersBaseBytes {
			return 0
		}

		return min((maxBytes-redeemVouchersBaseBytes)/redeemVouchersPerItemBytes, maxBatchAddresses)
	case BatchRedeemStreams:
		if maxBytes < redeemStreamsBaseBytes {
			return 0
		}

		return min((maxBytes-redeemStreamsBaseBytes)/redeemStreamsPerItemBytes, maxBatchAddresses)
	case BatchFund:
		return CapacityFund
	case BatchSweep:
		return CapacitySweep
	default:
		return 0
	}
}

func ComputeUnits(kind BatchKind, count int) uint32 {
	switch kind {
	case BatchRedeemVouchers:
		return RedeemVouchersBaseComputeUnits + uint32(count)*RedeemVouchersPerItemComputeUnits
	case BatchRedeemStreams:
		return RedeemStreamsBaseComputeUnits + uint32(count)*RedeemStreamsPerItemComputeUnits
	case BatchFund:
		return FundBaseComputeUnits + uint32(count)*FundPerItemComputeUnits
	case BatchSweep:
		return SweepBaseComputeUnits + uint32(count)*SweepPerItemComputeUnits
	default:
		return 0
	}
}

func SortedUniqueSubjects(subjects []solana.PublicKey) []solana.PublicKey {
	return solana.PublicKeySlice(slices.Clone(subjects)).Dedupe()
}

func SplitBatches(kind BatchKind, subjects []solana.PublicKey) [][]solana.PublicKey {
	return SplitBatchesFor(kind, DefaultMaxTransactionBytes, subjects)
}

func SplitBatchesFor(kind BatchKind, maxBytes int, subjects []solana.PublicKey) [][]solana.PublicKey {
	sorted := SortedUniqueSubjects(subjects)

	capacity := CapacityFor(kind, maxBytes)
	if capacity == 0 || len(sorted) == 0 {
		return nil
	}

	return slices.Collect(slices.Chunk(sorted, capacity))
}

func ValidateBatch(kind BatchKind, subjects []solana.PublicKey) error {
	if count, capacity := len(subjects), Capacity(kind); count > capacity {
		return fmt.Errorf("%w: %d subjects exceed %d", ErrBatchTooLarge, count, capacity)
	}

	for i := 1; i < len(subjects); i++ {
		if bytes.Compare(subjects[i-1][:], subjects[i][:]) >= 0 {
			return fmt.Errorf("%w: subject %d", dvpn.ErrBatchNotSorted, i)
		}
	}

	return nil
}
