use distributor_core::address::AddressError;
use distributor_vesting::unlock::VestingError;

#[derive(Debug, thiserror::Error)]
#[non_exhaustive]
pub enum DatasetError {
    #[error("invalid hex: {0}")]
    InvalidHex(#[from] hex::FromHexError),
    #[error("expected {expected} hex bytes, got {actual}")]
    InvalidHexLength { expected: usize, actual: usize },
    #[error("manifest path must be relative: {0}")]
    PathNotRelative(String),
    #[error("manifest path escapes the dataset dir: {0}")]
    PathEscapesDir(String),
    #[error("read {artifact}: {source}")]
    ReadFailed {
        artifact: String,
        #[source]
        source: std::io::Error,
    },
    #[error("parse {artifact}: {source}")]
    ParseFailed {
        artifact: String,
        #[source]
        source: serde_json::Error,
    },
    #[error("attestation schema_version {actual} != {expected} supported by this build")]
    UnsupportedSchemaVersion { expected: u16, actual: u16 },
    #[error("roots.json has {0} entries (> u16)")]
    RootCountTooLarge(usize),
    #[error(
        "attestation shard_count {claimed} != recomputed shard_count (roots.json length) {actual}"
    )]
    ShardCountMismatch { claimed: u16, actual: u16 },
    #[error("attestation shard_leaf_counts has {claimed} entries != {actual} shard roots")]
    ShardLeafCountsLengthMismatch { claimed: usize, actual: usize },
    #[error(
        "attestation shard_leaf_counts[{shard}] = {count} exceeds the per-shard cap ({cap}); \
         the tree-builder can never emit this, and `initialize_bitmap` would reject it mid-ceremony"
    )]
    ShardLeafCountTooLarge {
        shard: usize,
        count: u32,
        cap: usize,
    },
    #[error("roots_commitment mismatch")]
    RootsCommitmentMismatch,
    #[error("shard file hash mismatch: {0}")]
    ShardHashMismatch(String),
    #[error("manifest leaf_count {claimed} != {actual} for {file}")]
    LeafCountMismatch {
        claimed: u64,
        actual: u64,
        file: String,
    },
    #[error("shard {shard_id} has {count} entries (> u32::MAX)")]
    ShardEntryCountTooLarge { shard_id: u16, count: usize },
    #[error("leaf_count overflow")]
    LeafCountOverflow,
    #[error("manifest shard_id {0} out of range")]
    ShardIdOutOfRange(u16),
    #[error("shard {0} leaf count overflow")]
    ShardLeafCountSumOverflow(u16),
    #[error("total_claimable overflows u64 at {0}")]
    TotalClaimableOverflow(String),
    #[error("shard_id {0} out of range")]
    EntryShardIdOutOfRange(u16),
    #[error("proof failed for {0}")]
    InvalidProof(String),
    #[error("attestation leaf_count {claimed} != recomputed leaf_count {actual}")]
    AggregateLeafCountMismatch { claimed: u64, actual: u64 },
    #[error("attestation total_claimable {claimed} != recomputed total_claimable {actual}")]
    TotalClaimableMismatch { claimed: u64, actual: u64 },
    #[error(
        "attestation shard_leaf_counts disagree with the emitted shard files \
         (first divergence: shard {shard} claims {claimed}, files hold {actual})"
    )]
    ShardLeafCountsMismatch {
        shard: usize,
        claimed: u32,
        actual: u32,
    },
    #[error("claim_dataset_manifest_hash does not bind the emitted shard files")]
    ManifestHashMismatch,
    #[error(
        "manifest.json entries are not shard_id-sorted: entry {index} has shard_id {shard_id}, \
         which is less than the preceding entry's shard_id {prev_shard_id}"
    )]
    ManifestNotShardIdSorted {
        index: usize,
        shard_id: u16,
        prev_shard_id: u16,
    },
    #[error("locked > 0 with no vesting schedule for {0}")]
    MissingSchedule(String),
    #[error("schedule present on an entry with locked == 0: {0}")]
    UnexpectedSchedule(String),
    #[error("non-zero vesting_schedule_hash on an entry with locked == 0: {0}")]
    ScheduleHashNotZero(String),
    #[error("vesting_schedule_hash does not bind the entry's schedule: {0}")]
    ScheduleHashMismatch(String),
    #[error("invalid vesting schedule for {address}: {source}")]
    InvalidSchedule {
        address: String,
        #[source]
        source: VestingError,
    },
    #[error(
        "entry {address} carries shard_id {entry} inside the manifest item for shard {manifest}"
    )]
    EntryShardIdMismatch {
        address: String,
        manifest: u16,
        entry: u16,
    },
    #[error("shard {shard} has a duplicate leaf_index {leaf_index}")]
    DuplicateLeafIndex { shard: u16, leaf_index: u32 },
    #[error("shard {shard} leaf_index {leaf_index} is not below the shard's leaf count {count}")]
    LeafIndexOutOfRange {
        shard: u16,
        leaf_index: u32,
        count: u32,
    },
    #[error("invalid claimant address {address}: {source}")]
    InvalidAddress {
        address: String,
        #[source]
        source: AddressError,
    },
    #[error("duplicate claimant address: {0}")]
    DuplicateAddress(String),
    #[error("entry {address} claims shard {claimed} but its address derives to shard {actual}")]
    ShardIdMismatch {
        address: String,
        claimed: u16,
        actual: u16,
    },
    #[error("public key does not hash to address: {0}")]
    IdentityMismatch(String),
    #[error("explicit all-zero unrevealed public key (must be null): {0}")]
    UnrevealedPublicKeyForbidden(String),
    #[error("{artifact} exceeds {max} bytes")]
    ArtifactTooLarge { artifact: String, max: u64 },
    #[error("proof for {address} has {len} siblings (> {max})")]
    ProofTooLong {
        address: String,
        len: usize,
        max: usize,
    },
    #[error("attestation {field} is {len} bytes (> {max})")]
    AttestationStringTooLong {
        field: &'static str,
        len: usize,
        max: usize,
    },
}
