use std::collections::HashMap;
use std::io;
use std::path::Path;

use distributor_core::hash::keccak256;
use distributor_merkle::{BuiltLeaf, LedgerRecord, build_sharded_tree};
use distributor_vesting::schedule::{Schedule, ZERO_SCHEDULE_HASH};
use distributor_vesting::unlock::check_schedule;
use rayon::prelude::*;

use crate::ledger::ParsedRecord;
use crate::stage::Stage;

pub use distributor_dataset::{
    Attestation, ClaimEntry, ShardDataset, manifest_hash_of_json, require_valid_dataset,
};

#[derive(Debug)]
pub struct BuildOutput {
    pub roots: Vec<[u8; 32]>,
    pub roots_commitment: [u8; 32],
    pub attestation: Attestation,
    pub shards: Vec<ShardDataset>,
    pub shard_json: Vec<Vec<u8>>,
    pub ledger_content_hash: [u8; 32],
}

#[derive(Debug)]
pub enum BuildTreeError {
    TreeBuildFailed(String),
    InvalidSchedule(String),
}

pub fn build(
    records: &[ParsedRecord],
    ledger_content_hash: [u8; 32],
) -> Result<BuildOutput, BuildTreeError> {
    let check_stage = Stage::start("check-schedules");
    let mut schedule_by_address: HashMap<String, Schedule> = HashMap::new();
    let mut ledger_records: Vec<LedgerRecord> = Vec::with_capacity(records.len());
    for record in records {
        match &record.schedule {
            Some(schedule) => {
                check_schedule(schedule, record.locked_amount).map_err(|error| {
                    BuildTreeError::InvalidSchedule(format!(
                        "{:?} for {}",
                        error, record.bech32_address
                    ))
                })?;
                schedule_by_address.insert(record.bech32_address.clone(), schedule.clone());
            }
            None => {
                if record.locked_amount != 0 {
                    return Err(BuildTreeError::InvalidSchedule(format!(
                        "locked>0 with no schedule for {}",
                        record.bech32_address
                    )));
                }
            }
        }
        ledger_records.push(to_ledger_record(record));
    }
    check_stage.done(&[("records", &ledger_records.len())]);

    let tree_stage = Stage::start("build-sharded-tree");
    let tree = build_sharded_tree(ledger_records)
        .map_err(|error| BuildTreeError::TreeBuildFailed(format!("{error:?}")))?;
    tree_stage.done(&[("leaves", &tree.leaves.len())]);

    let group_stage = Stage::start("group-leaves");
    let mut groups: Vec<Vec<BuiltLeaf>> = Vec::new();
    for leaf in tree.leaves {
        match groups.last_mut() {
            Some(group)
                if group
                    .first()
                    .is_some_and(|head| head.shard_id == leaf.shard_id) =>
            {
                group.push(leaf)
            }
            _ => groups.push(vec![leaf]),
        }
    }
    let leaf_count = tree
        .leaf_counts
        .iter()
        .map(|count| u64::from(*count))
        .sum::<u64>();
    group_stage.done(&[("shards", &groups.len())]);

    let serialize_stage = Stage::start("serialize-shards");
    let built: Vec<(ShardDataset, Vec<u8>)> = groups
        .into_par_iter()
        .map(|group| {
            #[allow(clippy::expect_used)]
            let shard_id = group.first().expect("group is non-empty").shard_id;
            let entries: Vec<ClaimEntry> = group
                .into_iter()
                .map(|leaf| {
                    let schedule = schedule_by_address.get(&leaf.bech32_address).cloned();
                    ClaimEntry {
                        public_key: (leaf.compressed_public_key
                            != distributor_merkle::leaf::UNREVEALED_PUBKEY)
                            .then(|| hex::encode(leaf.compressed_public_key)),
                        liquid_amount: leaf.liquid_amount,
                        locked_amount: leaf.locked_amount,
                        schedule,
                        shard_id: leaf.shard_id,
                        leaf_index: leaf.leaf_index,
                        vesting_schedule_hash: hex::encode(leaf.vesting_schedule_hash),
                        proof: leaf.proof.iter().map(hex::encode).collect(),
                        bech32_address: leaf.bech32_address,
                    }
                })
                .collect();
            let json = serde_json::to_vec(&entries).expect("ShardDataset entries always serialize");
            (ShardDataset { shard_id, entries }, json)
        })
        .collect();
    let (shards, shard_json): (Vec<ShardDataset>, Vec<Vec<u8>>) = built.into_iter().unzip();
    serialize_stage.done(&[("shards", &shards.len())]);

    let roots_commitment = tree.roots_commitment;
    let manifest_stage = Stage::start("manifest-hash");
    let manifest_hash = manifest_hash_of_json(
        shards
            .iter()
            .zip(&shard_json)
            .map(|(shard, json)| (shard.shard_id, json.as_slice())),
    );
    manifest_stage.done(&[]);
    Ok(BuildOutput {
        attestation: Attestation {
            schema_version: distributor_dataset::ATTESTATION_SCHEMA_VERSION,
            roots_commitment: hex::encode(roots_commitment),
            shard_leaf_counts: tree.leaf_counts.clone(),
            total_claimable: tree.total_claimable,
            leaf_count,
            shard_count: distributor_core::consts::SHARD_COUNT,
            ledger_content_hash: hex::encode(ledger_content_hash),
            claim_dataset_manifest_hash: hex::encode(manifest_hash),
            git_commit: option_env!("GIT_COMMIT").unwrap_or("unknown").to_string(),
            tool_version: env!("CARGO_PKG_VERSION").to_string(),
        },
        roots: tree.roots,
        roots_commitment,
        shards,
        shard_json,
        ledger_content_hash,
    })
}

#[must_use]
pub fn to_ledger_record(record: &ParsedRecord) -> LedgerRecord {
    LedgerRecord {
        compressed_public_key: record.compressed_public_key,
        bech32_address: record.bech32_address.clone(),
        liquid_amount: record.liquid_amount,
        locked_amount: record.locked_amount,
        vesting_schedule_hash: record
            .schedule
            .as_ref()
            .map_or(ZERO_SCHEDULE_HASH, |schedule| schedule.hash()),
    }
}

pub fn write_output(dir: &Path, out: &BuildOutput) -> io::Result<()> {
    let claim_dir = dir.join("claim_dataset");
    if claim_dir
        .read_dir()
        .is_ok_and(|mut entries| entries.next().is_some())
    {
        return Err(io::Error::new(
            io::ErrorKind::AlreadyExists,
            format!(
                "{} is not empty; refusing to write into it (a stale shard file from a \
                 prior build could survive uncounted in the new manifest)",
                claim_dir.display()
            ),
        ));
    }
    std::fs::create_dir_all(&claim_dir)?;

    let write_stage = Stage::start("write-shards");
    let manifest = out
        .shards
        .par_iter()
        .zip(&out.shard_json)
        .map(|(shard, json)| {
            let path = claim_dir.join(format!("shard_{}.json", shard.shard_id));
            std::fs::write(&path, json)?;
            Ok(serde_json::json!({ "shard_id": shard.shard_id, "leaf_count": shard.entries.len(), "file_hash": hex::encode(keccak256(&[json])), "file": format!("claim_dataset/shard_{}.json", shard.shard_id) }))
        })
        .collect::<io::Result<Vec<serde_json::Value>>>()?;
    write_stage.done(&[("shards", &manifest.len())]);
    std::fs::write(
        dir.join("manifest.json"),
        serde_json::to_vec_pretty(&manifest)?,
    )?;
    let roots_hex: Vec<String> = out.roots.iter().map(hex::encode).collect();
    std::fs::write(
        dir.join("roots.json"),
        serde_json::to_vec_pretty(&roots_hex)?,
    )?;
    std::fs::write(
        dir.join("attestation.json"),
        serde_json::to_vec_pretty(&out.attestation)?,
    )?;
    Ok(())
}
