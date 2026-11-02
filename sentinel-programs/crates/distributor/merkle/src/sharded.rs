use std::collections::HashSet;

use distributor_core::address::{decode_bech32_address, encode_bech32_address};
use distributor_core::consts::{EMPTY_SHARD_ROOT, ROOTS_DOMAIN_SEPARATOR, SHARD_COUNT};
use distributor_core::hash::{hash160, keccak256};
use distributor_core::shard::shard_id;

use crate::leaf::{LeafFields, UNREVEALED_PUBKEY, leaf_hash};
use crate::tree::Levels;

pub const MAX_SHARD_LEAF_COUNT: usize = 65_536;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct LedgerRecord {
    pub compressed_public_key: Option<[u8; 33]>,
    pub bech32_address: String,
    pub liquid_amount: u64,
    pub locked_amount: u64,
    pub vesting_schedule_hash: [u8; 32],
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct BuiltLeaf {
    pub shard_id: u16,
    pub leaf_index: u32,
    pub address_bytes: [u8; 20],
    pub bech32_address: String,
    pub compressed_public_key: [u8; 33],
    pub liquid_amount: u64,
    pub locked_amount: u64,
    pub vesting_schedule_hash: [u8; 32],
    pub leaf_hash: [u8; 32],
    pub proof: Vec<[u8; 32]>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct BuiltTree {
    pub roots: Vec<[u8; 32]>,
    pub leaf_counts: Vec<u32>,
    pub roots_commitment: [u8; 32],
    pub total_claimable: u64,
    pub leaves: Vec<BuiltLeaf>,
}

#[derive(Debug, PartialEq, Eq, thiserror::Error)]
#[non_exhaustive]
pub enum BuildError {
    #[error("undecodable/non-canonical address: {address}")]
    InvalidAddress {
        address: String,
        #[source]
        source: distributor_core::address::AddressError,
    },
    #[error("duplicate address: {0}")]
    DuplicateAddress(String),
    #[error("public key does not hash to address: {0}")]
    IdentityMismatch(String),
    #[error("all-zero unrevealed public key supplied as a revealed key: {0}")]
    UnrevealedPublicKeyForbidden(String),
    #[error("shard {shard_id} has {count} leaves (> {MAX_SHARD_LEAF_COUNT})")]
    ShardLeafCountTooLarge { shard_id: u16, count: usize },
    #[error("record has neither liquid nor locked funds: {0}")]
    ZeroEntitlement(String),
    #[error("locked > 0 with all-zero vesting_schedule_hash (permanently unclaimable): {0}")]
    MissingScheduleHash(String),
    #[error("locked_amount == 0 with nonzero vesting_schedule_hash: {0}")]
    ScheduleHashNotZero(String),
    #[error("total_claimable overflows u64 at {0}")]
    TotalClaimableOverflow(String),
}

#[must_use]
#[allow(clippy::expect_used)]
pub fn roots_commitment(roots: &[[u8; 32]], leaf_counts: &[u32], total_claimable: u64) -> [u8; 32] {
    let roots_len = u32::try_from(roots.len())
        .expect("SHARD_COUNT roots fits in u32")
        .to_le_bytes();
    let leaf_counts_len = u32::try_from(leaf_counts.len())
        .expect("SHARD_COUNT leaf_counts fits in u32")
        .to_le_bytes();
    let mut leaf_counts_le: Vec<u8> = Vec::with_capacity(leaf_counts.len().saturating_mul(4));
    for leaf_count in leaf_counts {
        leaf_counts_le.extend_from_slice(&leaf_count.to_le_bytes());
    }
    let total_le = total_claimable.to_le_bytes();
    let mut parts: Vec<&[u8]> = Vec::with_capacity(roots.len().saturating_add(5));
    parts.push(ROOTS_DOMAIN_SEPARATOR);
    parts.push(&roots_len);
    for root in roots {
        parts.push(root);
    }
    parts.push(&leaf_counts_len);
    parts.push(&leaf_counts_le);
    parts.push(&total_le);
    keccak256(&parts)
}

struct PreparedRecord {
    address_bytes: [u8; 20],
    shard_id: u16,
    record: LedgerRecord,
    compressed_public_key: [u8; 33],
}

struct BuiltShard {
    root: [u8; 32],
    leaf_count: u32,
    leaves: Vec<BuiltLeaf>,
}

fn prepare_record(record: LedgerRecord) -> Result<PreparedRecord, BuildError> {
    if record.liquid_amount == 0 && record.locked_amount == 0 {
        return Err(BuildError::ZeroEntitlement(record.bech32_address));
    }
    if record.locked_amount > 0 && record.vesting_schedule_hash == [0u8; 32] {
        return Err(BuildError::MissingScheduleHash(record.bech32_address));
    }
    if record.locked_amount == 0 && record.vesting_schedule_hash != [0u8; 32] {
        return Err(BuildError::ScheduleHashNotZero(record.bech32_address));
    }

    let address_bytes = decode_bech32_address(&record.bech32_address).map_err(|source| {
        BuildError::InvalidAddress {
            address: record.bech32_address.clone(),
            source,
        }
    })?;
    let compressed_public_key = match record.compressed_public_key {
        Some(public_key) => {
            if public_key == UNREVEALED_PUBKEY {
                return Err(BuildError::UnrevealedPublicKeyForbidden(
                    record.bech32_address,
                ));
            }
            if encode_bech32_address(&hash160(&public_key)) != record.bech32_address {
                return Err(BuildError::IdentityMismatch(record.bech32_address));
            }
            public_key
        }
        None => UNREVEALED_PUBKEY,
    };
    let derived_shard_id = shard_id(&address_bytes);
    Ok(PreparedRecord {
        address_bytes,
        shard_id: derived_shard_id,
        record,
        compressed_public_key,
    })
}

fn build_shard(shard_id: u16, mut records: Vec<PreparedRecord>) -> Result<BuiltShard, BuildError> {
    let count = records.len();
    if count == 0 {
        return Ok(BuiltShard {
            root: EMPTY_SHARD_ROOT,
            leaf_count: 0,
            leaves: Vec::new(),
        });
    }
    if count > MAX_SHARD_LEAF_COUNT {
        return Err(BuildError::ShardLeafCountTooLarge { shard_id, count });
    }

    let leaf_count =
        u32::try_from(count).map_err(|_| BuildError::ShardLeafCountTooLarge { shard_id, count })?;
    records.sort_by_key(|record| record.address_bytes);

    let mut leaf_hashes: Vec<[u8; 32]> = Vec::with_capacity(count);
    let mut staged: Vec<BuiltLeaf> = Vec::with_capacity(count);
    for (leaf_index, prepared) in (0..leaf_count).zip(records) {
        let hash = leaf_hash(&LeafFields {
            shard_id,
            leaf_index,
            compressed_public_key: prepared.compressed_public_key,
            bech32_address: &prepared.record.bech32_address,
            liquid_amount: prepared.record.liquid_amount,
            locked_amount: prepared.record.locked_amount,
            vesting_schedule_hash: prepared.record.vesting_schedule_hash,
        });
        leaf_hashes.push(hash);
        staged.push(BuiltLeaf {
            shard_id,
            leaf_index,
            address_bytes: prepared.address_bytes,
            bech32_address: prepared.record.bech32_address,
            compressed_public_key: prepared.compressed_public_key,
            liquid_amount: prepared.record.liquid_amount,
            locked_amount: prepared.record.locked_amount,
            vesting_schedule_hash: prepared.record.vesting_schedule_hash,
            leaf_hash: hash,
            proof: Vec::new(),
        });
    }

    let levels = Levels::build(&leaf_hashes);
    let root = levels.root();
    for (index, leaf) in staged.iter_mut().enumerate() {
        leaf.proof = levels.proof(index);
    }
    Ok(BuiltShard {
        root,
        leaf_count,
        leaves: staged,
    })
}

pub fn build_sharded_tree(records: Vec<LedgerRecord>) -> Result<BuiltTree, BuildError> {
    let prepared_results: Vec<Result<PreparedRecord, BuildError>> =
        records.into_iter().map(prepare_record).collect();

    let mut seen: HashSet<[u8; 20]> = HashSet::with_capacity(prepared_results.len());
    let mut total_claimable: u64 = 0;
    let mut prepared: Vec<PreparedRecord> = Vec::with_capacity(prepared_results.len());
    for res in prepared_results {
        let prepared_record = res?;
        total_claimable = total_claimable
            .checked_add(prepared_record.record.liquid_amount)
            .and_then(|sum| sum.checked_add(prepared_record.record.locked_amount))
            .ok_or_else(|| {
                BuildError::TotalClaimableOverflow(prepared_record.record.bech32_address.clone())
            })?;
        if !seen.insert(prepared_record.address_bytes) {
            return Err(BuildError::DuplicateAddress(
                prepared_record.record.bech32_address,
            ));
        }
        prepared.push(prepared_record);
    }

    let mut by_shard: Vec<Vec<PreparedRecord>> = (0..SHARD_COUNT).map(|_| Vec::new()).collect();
    for prepared_record in prepared {
        #[allow(clippy::indexing_slicing)]
        by_shard[usize::from(prepared_record.shard_id)].push(prepared_record);
    }

    let results: Vec<Result<BuiltShard, BuildError>> = (0..SHARD_COUNT)
        .zip(by_shard)
        .map(|(shard_id, records)| build_shard(shard_id, records))
        .collect();
    let shards = results
        .into_iter()
        .collect::<Result<Vec<BuiltShard>, BuildError>>()?;
    let (roots, leaf_counts): (Vec<[u8; 32]>, Vec<u32>) = shards
        .iter()
        .map(|shard| (shard.root, shard.leaf_count))
        .unzip();
    let out_leaves: Vec<BuiltLeaf> = shards.into_iter().flat_map(|shard| shard.leaves).collect();

    let commitment = roots_commitment(&roots, &leaf_counts, total_claimable);
    Ok(BuiltTree {
        roots,
        leaf_counts,
        roots_commitment: commitment,
        total_claimable,
        leaves: out_leaves,
    })
}
