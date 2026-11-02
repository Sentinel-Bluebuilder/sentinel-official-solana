use crate::{
    ATTESTATION_SCHEMA_VERSION, Attestation, ClaimEntry, DatasetError,
    MAX_ATTESTATION_STRING_BYTES, MAX_INDEX_FILE_BYTES, MAX_PROOF_SIBLING_COUNT,
    MAX_SHARD_FILE_BYTES, decode_hex_array,
};
use distributor_core::address::{decode_bech32_address, encode_bech32_address};
use distributor_core::hash::{hash160, keccak256};
use distributor_merkle::leaf::UNREVEALED_PUBKEY;
use distributor_merkle::roots_commitment;
use distributor_vesting::schedule::ZERO_SCHEDULE_HASH;
use distributor_vesting::unlock::check_schedule;
use rayon::prelude::*;
use serde::Deserialize;
use std::collections::HashSet;
use std::path::{Component, Path, PathBuf};

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ManifestEntry {
    file: String,
    shard_id: u16,
    file_hash: String,
    leaf_count: u64,
}

fn confine(dir: &Path, file: &str) -> Result<PathBuf, DatasetError> {
    let rel = Path::new(file);

    if rel.is_absolute() {
        return Err(DatasetError::PathNotRelative(file.to_string()));
    }
    for component in rel.components() {
        match component {
            Component::ParentDir => {
                return Err(DatasetError::PathEscapesDir(file.to_string()));
            }
            Component::Prefix(_) | Component::RootDir => {
                return Err(DatasetError::PathNotRelative(file.to_string()));
            }
            Component::CurDir | Component::Normal(_) => {}
        }
    }
    Ok(dir.join(rel))
}

struct ShardVerifyResult {
    shard_id: u16,
    file_hash: [u8; 32],
    leaf_count: u64,
    entry_count: u32,
    total_claimable: u64,
    addresses: Vec<[u8; 20]>,
    leaf_indices: Vec<u32>,
}

fn require_valid_shard_file(
    dir: &Path,
    item: &ManifestEntry,
    roots: &[[u8; 32]],
    attestation: &Attestation,
) -> Result<ShardVerifyResult, DatasetError> {
    let path = confine(dir, &item.file)?;
    let bytes = read_capped(&path, &item.file, MAX_SHARD_FILE_BYTES)?;
    let file_hash = keccak256(&[&bytes]);
    if file_hash != decode_hex_array::<32>(&item.file_hash)? {
        return Err(DatasetError::ShardHashMismatch(item.file.clone()));
    }
    let entries: Vec<ClaimEntry> =
        serde_json::from_slice(&bytes).map_err(|source| DatasetError::ParseFailed {
            artifact: item.file.clone(),
            source,
        })?;
    let actual_count =
        u64::try_from(entries.len()).map_err(|_| DatasetError::ShardEntryCountTooLarge {
            shard_id: item.shard_id,
            count: entries.len(),
        })?;
    if item.leaf_count != actual_count {
        return Err(DatasetError::LeafCountMismatch {
            claimed: item.leaf_count,
            actual: actual_count,
            file: item.file.clone(),
        });
    }

    if usize::from(item.shard_id) >= roots.len() {
        return Err(DatasetError::ShardIdOutOfRange(item.shard_id));
    }
    let entry_count =
        u32::try_from(entries.len()).map_err(|_| DatasetError::ShardEntryCountTooLarge {
            shard_id: item.shard_id,
            count: entries.len(),
        })?;
    let mut seen_addresses: HashSet<[u8; 20]> = HashSet::new();
    let mut seen_leaf_indices: HashSet<u32> = HashSet::new();
    let mut addresses = Vec::with_capacity(entries.len());
    let mut leaf_indices = Vec::with_capacity(entries.len());
    let mut total_claimable: u64 = 0;
    for entry in &entries {
        if entry.shard_id != item.shard_id {
            return Err(DatasetError::EntryShardIdMismatch {
                address: entry.bech32_address.clone(),
                manifest: item.shard_id,
                entry: entry.shard_id,
            });
        }
        let address_bytes = decode_bech32_address(&entry.bech32_address).map_err(|source| {
            DatasetError::InvalidAddress {
                address: entry.bech32_address.clone(),
                source,
            }
        })?;
        if !seen_addresses.insert(address_bytes) {
            return Err(DatasetError::DuplicateAddress(entry.bech32_address.clone()));
        }
        let derived_shard = distributor_core::shard::shard_id(&address_bytes);
        if derived_shard != entry.shard_id {
            return Err(DatasetError::ShardIdMismatch {
                address: entry.bech32_address.clone(),
                claimed: entry.shard_id,
                actual: derived_shard,
            });
        }
        if let Some(public_key_hex) = &entry.public_key {
            let compressed_public_key = decode_hex_array::<33>(public_key_hex)?;
            if compressed_public_key == UNREVEALED_PUBKEY {
                return Err(DatasetError::UnrevealedPublicKeyForbidden(
                    entry.bech32_address.clone(),
                ));
            }
            if encode_bech32_address(&hash160(&compressed_public_key)) != entry.bech32_address {
                return Err(DatasetError::IdentityMismatch(entry.bech32_address.clone()));
            }
        }
        let schedule_hash = decode_hex_array::<32>(&entry.vesting_schedule_hash)?;
        match (&entry.schedule, entry.locked_amount) {
            (None, 0) => {
                if schedule_hash != ZERO_SCHEDULE_HASH {
                    return Err(DatasetError::ScheduleHashNotZero(
                        entry.bech32_address.clone(),
                    ));
                }
            }
            (None, _) => return Err(DatasetError::MissingSchedule(entry.bech32_address.clone())),
            (Some(_), 0) => {
                return Err(DatasetError::UnexpectedSchedule(
                    entry.bech32_address.clone(),
                ));
            }
            (Some(schedule), locked) => {
                if schedule.hash() != schedule_hash {
                    return Err(DatasetError::ScheduleHashMismatch(
                        entry.bech32_address.clone(),
                    ));
                }

                check_schedule(schedule, locked).map_err(|source| {
                    DatasetError::InvalidSchedule {
                        address: entry.bech32_address.clone(),
                        source,
                    }
                })?;
            }
        }

        let claimed = attestation
            .shard_leaf_counts
            .get(usize::from(entry.shard_id))
            .copied()
            .ok_or(DatasetError::EntryShardIdOutOfRange(entry.shard_id))?;
        if entry.leaf_index >= claimed {
            return Err(DatasetError::LeafIndexOutOfRange {
                shard: entry.shard_id,
                leaf_index: entry.leaf_index,
                count: claimed,
            });
        }
        if !seen_leaf_indices.insert(entry.leaf_index) {
            return Err(DatasetError::DuplicateLeafIndex {
                shard: entry.shard_id,
                leaf_index: entry.leaf_index,
            });
        }
        total_claimable = total_claimable
            .checked_add(entry.liquid_amount)
            .and_then(|sum| sum.checked_add(entry.locked_amount))
            .ok_or_else(|| DatasetError::TotalClaimableOverflow(entry.bech32_address.clone()))?;
        if entry.proof.len() > MAX_PROOF_SIBLING_COUNT {
            return Err(DatasetError::ProofTooLong {
                address: entry.bech32_address.clone(),
                len: entry.proof.len(),
                max: MAX_PROOF_SIBLING_COUNT,
            });
        }
        let proof: Vec<[u8; 32]> = entry
            .proof
            .iter()
            .map(|hex_proof| decode_hex_array::<32>(hex_proof))
            .collect::<Result<_, DatasetError>>()?;
        let root = roots
            .get(usize::from(entry.shard_id))
            .ok_or(DatasetError::EntryShardIdOutOfRange(entry.shard_id))?;
        if !distributor_merkle::tree::is_valid_proof(&entry.leaf_hash()?, &proof, root) {
            return Err(DatasetError::InvalidProof(entry.bech32_address.clone()));
        }
        addresses.push(address_bytes);
        leaf_indices.push(entry.leaf_index);
    }
    Ok(ShardVerifyResult {
        shard_id: item.shard_id,
        file_hash,
        leaf_count: actual_count,
        entry_count,
        total_claimable,
        addresses,
        leaf_indices,
    })
}

pub fn require_valid_dataset(dir: &Path) -> Result<(), DatasetError> {
    let roots_hex: Vec<String> = read_json(dir, "roots.json")?;
    let roots: Vec<[u8; 32]> = roots_hex
        .iter()
        .map(|root_hex| decode_hex_array::<32>(root_hex))
        .collect::<Result<_, DatasetError>>()?;
    let attestation: Attestation = read_json(dir, "attestation.json")?;
    if attestation.schema_version != ATTESTATION_SCHEMA_VERSION {
        return Err(DatasetError::UnsupportedSchemaVersion {
            expected: ATTESTATION_SCHEMA_VERSION,
            actual: attestation.schema_version,
        });
    }
    for (field, value) in [
        ("git_commit", &attestation.git_commit),
        ("tool_version", &attestation.tool_version),
    ] {
        if value.len() > MAX_ATTESTATION_STRING_BYTES {
            return Err(DatasetError::AttestationStringTooLong {
                field,
                len: value.len(),
                max: MAX_ATTESTATION_STRING_BYTES,
            });
        }
    }

    let recomputed_shard_count =
        u16::try_from(roots.len()).map_err(|_| DatasetError::RootCountTooLarge(roots.len()))?;
    if attestation.shard_count != recomputed_shard_count {
        return Err(DatasetError::ShardCountMismatch {
            claimed: attestation.shard_count,
            actual: recomputed_shard_count,
        });
    }
    if attestation.shard_leaf_counts.len() != roots.len() {
        return Err(DatasetError::ShardLeafCountsLengthMismatch {
            claimed: attestation.shard_leaf_counts.len(),
            actual: roots.len(),
        });
    }

    let cap = u64::try_from(distributor_merkle::sharded::MAX_SHARD_LEAF_COUNT).unwrap_or(u64::MAX);
    if let Some((shard, shard_leaf_count)) = attestation
        .shard_leaf_counts
        .iter()
        .enumerate()
        .find(|(_, shard_leaf_count)| u64::from(**shard_leaf_count) > cap)
    {
        return Err(DatasetError::ShardLeafCountTooLarge {
            shard,
            count: *shard_leaf_count,
            cap: distributor_merkle::sharded::MAX_SHARD_LEAF_COUNT,
        });
    }
    if decode_hex_array::<32>(&attestation.roots_commitment)?
        != roots_commitment(
            &roots,
            &attestation.shard_leaf_counts,
            attestation.total_claimable,
        )
    {
        return Err(DatasetError::RootsCommitmentMismatch);
    }

    decode_hex_array::<32>(&attestation.ledger_content_hash)?;
    let expected_manifest_hash = decode_hex_array::<32>(&attestation.claim_dataset_manifest_hash)?;
    let manifest: Vec<ManifestEntry> = read_json(dir, "manifest.json")?;

    for (position, pair) in manifest.windows(2).enumerate() {
        let [prev, next] = pair else {
            continue;
        };
        if next.shard_id < prev.shard_id {
            return Err(DatasetError::ManifestNotShardIdSorted {
                index: position.saturating_add(1),
                shard_id: next.shard_id,
                prev_shard_id: prev.shard_id,
            });
        }
    }

    let results: Vec<Result<ShardVerifyResult, DatasetError>> = manifest
        .par_iter()
        .map(|item| require_valid_shard_file(dir, item, &roots, &attestation))
        .collect();
    let per_shard: Vec<ShardVerifyResult> = results.into_iter().collect::<Result<Vec<_>, _>>()?;

    let mut ordered: Vec<&ShardVerifyResult> = per_shard.iter().collect();
    ordered.sort_by_key(|result| result.shard_id);

    let mut manifest_preimage: Vec<u8> = Vec::new();

    let mut recomputed_leaf_count: u64 = 0;
    let mut recomputed_total_claimable: u64 = 0;

    let mut recomputed_shard_counts = vec![0u32; roots.len()];
    let mut seen_leaf_indices: HashSet<(u16, u32)> = HashSet::new();
    let mut seen_addresses: HashSet<[u8; 20]> = HashSet::new();
    for result in ordered {
        manifest_preimage.extend_from_slice(&result.shard_id.to_le_bytes());
        manifest_preimage.extend_from_slice(&result.file_hash);
        recomputed_leaf_count = recomputed_leaf_count
            .checked_add(result.leaf_count)
            .ok_or(DatasetError::LeafCountOverflow)?;
        let slot = recomputed_shard_counts
            .get_mut(usize::from(result.shard_id))
            .ok_or(DatasetError::ShardIdOutOfRange(result.shard_id))?;
        *slot = slot
            .checked_add(result.entry_count)
            .ok_or(DatasetError::ShardLeafCountSumOverflow(result.shard_id))?;

        for (&address_bytes, &leaf_index) in result.addresses.iter().zip(&result.leaf_indices) {
            if !seen_addresses.insert(address_bytes) {
                return Err(DatasetError::DuplicateAddress(encode_bech32_address(
                    &address_bytes,
                )));
            }
            if !seen_leaf_indices.insert((result.shard_id, leaf_index)) {
                return Err(DatasetError::DuplicateLeafIndex {
                    shard: result.shard_id,
                    leaf_index,
                });
            }
        }

        recomputed_total_claimable = recomputed_total_claimable
            .checked_add(result.total_claimable)
            .ok_or_else(|| {
                DatasetError::TotalClaimableOverflow(
                    result
                        .addresses
                        .first()
                        .map(encode_bech32_address)
                        .unwrap_or_default(),
                )
            })?;
    }
    if recomputed_leaf_count != attestation.leaf_count {
        return Err(DatasetError::AggregateLeafCountMismatch {
            claimed: attestation.leaf_count,
            actual: recomputed_leaf_count,
        });
    }
    if recomputed_total_claimable != attestation.total_claimable {
        return Err(DatasetError::TotalClaimableMismatch {
            claimed: attestation.total_claimable,
            actual: recomputed_total_claimable,
        });
    }
    if recomputed_shard_counts != attestation.shard_leaf_counts {
        let (shard, claimed, actual) = attestation
            .shard_leaf_counts
            .iter()
            .zip(&recomputed_shard_counts)
            .enumerate()
            .find(|(_, (left, right))| left != right)
            .map(|(position, (left, right))| (position, *left, *right))
            .unwrap_or((0, 0, 0));
        return Err(DatasetError::ShardLeafCountsMismatch {
            shard,
            claimed,
            actual,
        });
    }
    if keccak256(&[&manifest_preimage]) != expected_manifest_hash {
        return Err(DatasetError::ManifestHashMismatch);
    }
    Ok(())
}

fn read_capped(path: &Path, artifact: &str, cap: u64) -> Result<Vec<u8>, DatasetError> {
    use std::io::Read;
    let file = std::fs::File::open(path).map_err(|source| DatasetError::ReadFailed {
        artifact: artifact.to_string(),
        source,
    })?;
    let mut buf = Vec::new();
    file.take(cap.saturating_add(1))
        .read_to_end(&mut buf)
        .map_err(|source| DatasetError::ReadFailed {
            artifact: artifact.to_string(),
            source,
        })?;
    if u64::try_from(buf.len()).unwrap_or(u64::MAX) > cap {
        return Err(DatasetError::ArtifactTooLarge {
            artifact: artifact.to_string(),
            max: cap,
        });
    }
    Ok(buf)
}

fn read_json<T: for<'d> Deserialize<'d>>(dir: &Path, name: &str) -> Result<T, DatasetError> {
    let bytes = read_capped(&dir.join(name), name, MAX_INDEX_FILE_BYTES)?;
    serde_json::from_slice(&bytes).map_err(|source| DatasetError::ParseFailed {
        artifact: name.to_string(),
        source,
    })
}
