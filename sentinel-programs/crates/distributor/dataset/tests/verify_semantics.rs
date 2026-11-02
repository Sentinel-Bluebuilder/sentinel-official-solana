use std::path::Path;

use distributor_core::hash::keccak256;
use distributor_dataset::{ClaimEntry, DatasetError};
use distributor_vesting::schedule::{Schedule, ScheduleKind};

fn address_bytes(seed: u8) -> [u8; 20] {
    [seed; 20]
}

fn address(seed: u8) -> String {
    distributor_core::address::encode_bech32_address(&address_bytes(seed))
}

fn public_key(seed: u8) -> [u8; 33] {
    let mut public_key_bytes = [seed; 33];
    public_key_bytes[0] = 0x02;
    public_key_bytes
}

fn zero_hash_hex() -> String {
    hex::encode([0u8; 32])
}

fn address_bytes_in_shard(seed: u8, shard: u16) -> [u8; 20] {
    let mut candidate = [seed; 20];
    for salt in 0u16..=u16::MAX {
        let [high, low] = salt.to_be_bytes();
        candidate[18] = high;
        candidate[19] = low;
        if distributor_core::shard::shard_id(&candidate) == shard {
            return candidate;
        }
    }
    panic!("no salt landed an address in shard {shard} for seed {seed}");
}

fn public_key_in_shard(seed: u8, shard: u16) -> [u8; 33] {
    let mut public_key_bytes = public_key(seed);
    for salt in 0u16..=u16::MAX {
        let [high, low] = salt.to_be_bytes();
        public_key_bytes[31] = high;
        public_key_bytes[32] = low;
        if distributor_core::shard::shard_id(&distributor_core::hash::hash160(&public_key_bytes))
            == shard
        {
            return public_key_bytes;
        }
    }
    panic!("no salt landed a public-key-hash address in shard {shard} for seed {seed}");
}

fn unrevealed_liquid_entry(seed: u8, shard_id: u16, leaf_index: u32, liquid: u64) -> ClaimEntry {
    ClaimEntry {
        bech32_address: distributor_core::address::encode_bech32_address(&address_bytes_in_shard(
            seed, shard_id,
        )),
        public_key: None,
        liquid_amount: liquid,
        locked_amount: 0,
        schedule: None,
        shard_id,
        leaf_index,
        vesting_schedule_hash: zero_hash_hex(),
        proof: vec![],
    }
}

fn revealed_liquid_entry(seed: u8, shard_id: u16, leaf_index: u32, liquid: u64) -> ClaimEntry {
    let public_key_bytes = public_key_in_shard(seed, shard_id);
    ClaimEntry {
        bech32_address: distributor_core::address::encode_bech32_address(
            &distributor_core::hash::hash160(&public_key_bytes),
        ),
        public_key: Some(hex::encode(public_key_bytes)),
        liquid_amount: liquid,
        locked_amount: 0,
        schedule: None,
        shard_id,
        leaf_index,
        vesting_schedule_hash: zero_hash_hex(),
        proof: vec![],
    }
}

fn delayed_schedule(total: u64) -> Schedule {
    Schedule {
        kind: ScheduleKind::Delayed,
        start_at: 0,
        end_at: 1_700_000_000,
        periods: vec![],
        total,
    }
}

fn unrevealed_locked_entry(seed: u8, shard_id: u16, leaf_index: u32, locked: u64) -> ClaimEntry {
    let schedule = delayed_schedule(locked);
    ClaimEntry {
        bech32_address: distributor_core::address::encode_bech32_address(&address_bytes_in_shard(
            seed, shard_id,
        )),
        public_key: None,
        liquid_amount: 1,
        locked_amount: locked,
        schedule: Some(schedule.clone()),
        shard_id,
        leaf_index,
        vesting_schedule_hash: hex::encode(schedule.hash()),
        proof: vec![],
    }
}

fn seal_shard(entries: &mut [ClaimEntry]) -> [u8; 32] {
    let hashes: Vec<[u8; 32]> = entries
        .iter()
        .map(|entry| entry.leaf_hash().unwrap())
        .collect();
    let levels = distributor_merkle::tree::Levels::build(&hashes);
    for (index, entry) in entries.iter_mut().enumerate() {
        entry.proof = levels.proof(index).iter().map(hex::encode).collect();
    }
    levels.root()
}

fn write_dataset(
    dir: &Path,
    roots: &[[u8; 32]],
    shard_leaf_counts: &[u32],
    shards: &[(u16, Vec<ClaimEntry>)],
) {
    let mut manifest = Vec::new();
    let mut manifest_preimage: Vec<u8> = Vec::new();
    let mut total_claimable: u64 = 0;
    let mut leaf_count: u64 = 0;
    for (sid, entries) in shards {
        let json = serde_json::to_vec(entries).unwrap();
        let file = format!("shard_{sid}.json");
        std::fs::write(dir.join(&file), &json).unwrap();
        let file_hash = keccak256(&[&json]);
        manifest_preimage.extend_from_slice(&sid.to_le_bytes());
        manifest_preimage.extend_from_slice(&file_hash);
        manifest.push(serde_json::json!({
            "file": file,
            "shard_id": sid,
            "file_hash": hex::encode(file_hash),
            "leaf_count": entries.len(),
        }));
        leaf_count += u64::try_from(entries.len()).unwrap();
        for entry in entries {
            total_claimable += entry.liquid_amount + entry.locked_amount;
        }
    }
    std::fs::write(
        dir.join("manifest.json"),
        serde_json::to_vec(&manifest).unwrap(),
    )
    .unwrap();
    let roots_hex: Vec<String> = roots.iter().map(hex::encode).collect();
    std::fs::write(
        dir.join("roots.json"),
        serde_json::to_vec(&roots_hex).unwrap(),
    )
    .unwrap();
    let attestation = serde_json::json!({
        "schema_version": 3,
        "roots_commitment":
            hex::encode(distributor_merkle::roots_commitment(roots, shard_leaf_counts, total_claimable)),
        "shard_leaf_counts": shard_leaf_counts,
        "total_claimable": total_claimable.to_string(),
        "leaf_count": leaf_count,
        "shard_count": u16::try_from(roots.len()).unwrap(),
        "ledger_content_hash": hex::encode([0u8; 32]),
        "claim_dataset_manifest_hash": hex::encode(keccak256(&[&manifest_preimage])),
        "git_commit": "test",
        "tool_version": "test",
    });
    std::fs::write(
        dir.join("attestation.json"),
        serde_json::to_vec(&attestation).unwrap(),
    )
    .unwrap();
}

fn write_single_shard(dir: &Path, mut entries: Vec<ClaimEntry>) {
    let root = seal_shard(&mut entries);
    let count = u32::try_from(entries.len()).unwrap();
    write_dataset(dir, &[root], &[count], &[(0, entries)]);
}

#[test]
fn a_semantically_valid_dataset_verifies() {
    let dir = tempfile::tempdir().unwrap();
    let mut shard0 = vec![
        unrevealed_liquid_entry(1, 0, 0, 5),
        unrevealed_locked_entry(2, 0, 1, 10),
        revealed_liquid_entry(3, 0, 2, 7),
    ];
    let mut shard1 = vec![unrevealed_liquid_entry(4, 1, 0, 9)];
    let root0 = seal_shard(&mut shard0);
    let root1 = seal_shard(&mut shard1);
    write_dataset(
        dir.path(),
        &[root0, root1],
        &[3, 1],
        &[(0, shard0), (1, shard1)],
    );
    distributor_dataset::require_valid_dataset(dir.path()).unwrap();
}

#[test]
fn a_manifest_permuted_out_of_shard_id_order_is_rejected() {
    let dir = tempfile::tempdir().unwrap();
    let mut shard0 = vec![unrevealed_liquid_entry(1, 0, 0, 5)];
    let mut shard1 = vec![unrevealed_liquid_entry(4, 1, 0, 9)];
    let root0 = seal_shard(&mut shard0);
    let root1 = seal_shard(&mut shard1);
    write_dataset(
        dir.path(),
        &[root0, root1],
        &[1, 1],
        &[(0, shard0), (1, shard1)],
    );
    distributor_dataset::require_valid_dataset(dir.path()).unwrap();

    tamper_manifest(dir.path(), |json| {
        json.as_array_mut().unwrap().reverse();
    });
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(err, DatasetError::ManifestNotShardIdSorted { .. }),
        "got: {err:?}"
    );
}

#[test]
fn locked_without_schedule_is_rejected() {
    let dir = tempfile::tempdir().unwrap();
    let mut entry = unrevealed_liquid_entry(1, 0, 0, 5);
    entry.locked_amount = 10;
    entry.vesting_schedule_hash = hex::encode([7u8; 32]);
    write_single_shard(dir.path(), vec![entry]);
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(err, DatasetError::MissingSchedule(_)),
        "got: {err:?}"
    );
}

#[test]
fn schedule_on_a_fully_liquid_entry_is_rejected() {
    let dir = tempfile::tempdir().unwrap();
    let schedule = delayed_schedule(10);
    let mut entry = unrevealed_liquid_entry(1, 0, 0, 5);
    entry.schedule = Some(schedule.clone());
    entry.vesting_schedule_hash = hex::encode(schedule.hash());
    write_single_shard(dir.path(), vec![entry]);
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(err, DatasetError::UnexpectedSchedule(_)),
        "got: {err:?}"
    );
}

#[test]
fn nonzero_schedule_hash_without_locked_is_rejected() {
    let dir = tempfile::tempdir().unwrap();
    let mut entry = unrevealed_liquid_entry(1, 0, 0, 5);
    entry.vesting_schedule_hash = hex::encode([7u8; 32]);
    write_single_shard(dir.path(), vec![entry]);
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(err, DatasetError::ScheduleHashNotZero(_)),
        "got: {err:?}"
    );
}

#[test]
fn schedule_hash_not_matching_the_schedule_is_rejected() {
    let dir = tempfile::tempdir().unwrap();
    let mut entry = unrevealed_locked_entry(1, 0, 0, 10);
    entry.vesting_schedule_hash = hex::encode(delayed_schedule(11).hash());
    write_single_shard(dir.path(), vec![entry]);
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(err, DatasetError::ScheduleHashMismatch(_)),
        "got: {err:?}"
    );
}

#[test]
fn schedule_total_diverging_from_locked_is_rejected() {
    let dir = tempfile::tempdir().unwrap();
    let schedule = delayed_schedule(9);
    let mut entry = unrevealed_locked_entry(1, 0, 0, 10);
    entry.schedule = Some(schedule.clone());
    entry.vesting_schedule_hash = hex::encode(schedule.hash());
    write_single_shard(dir.path(), vec![entry]);
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(
            err,
            DatasetError::InvalidSchedule {
                source: distributor_vesting::unlock::VestingError::TotalMismatch,
                ..
            }
        ),
        "got: {err:?}"
    );
}

#[test]
fn an_entry_shard_id_diverging_from_its_manifest_item_is_rejected() {
    let dir = tempfile::tempdir().unwrap();
    let host = unrevealed_liquid_entry(1, 0, 0, 5);
    let smuggled = unrevealed_liquid_entry(2, 1, 0, 7);

    let roots = [host.leaf_hash().unwrap(), smuggled.leaf_hash().unwrap()];
    write_dataset(dir.path(), &roots, &[2, 0], &[(0, vec![host, smuggled])]);
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(err, DatasetError::EntryShardIdMismatch { .. }),
        "got: {err:?}"
    );
}

#[test]
fn an_entry_filed_under_a_shard_its_address_does_not_derive_to_is_rejected() {
    let dir = tempfile::tempdir().unwrap();
    let real = distributor_core::shard::shard_id(&address_bytes(1));

    let filed_under = if real == 0 { 1 } else { 0 };

    let mut entries = vec![ClaimEntry {
        bech32_address: address(1),
        public_key: None,
        liquid_amount: 5,
        locked_amount: 0,
        schedule: None,
        shard_id: filed_under,
        leaf_index: 0,
        vesting_schedule_hash: zero_hash_hex(),
        proof: vec![],
    }];
    let root = seal_shard(&mut entries);
    let mut roots = vec![[0u8; 32]; 2];
    let mut shard_leaf_counts = vec![0u32; 2];
    roots[usize::from(filed_under)] = root;
    shard_leaf_counts[usize::from(filed_under)] = 1;
    write_dataset(
        dir.path(),
        &roots,
        &shard_leaf_counts,
        &[(filed_under, entries)],
    );
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(err, DatasetError::ShardIdMismatch { .. }),
        "got: {err:?}"
    );
}

#[test]
fn a_duplicate_leaf_index_is_rejected() {
    let dir = tempfile::tempdir().unwrap();
    write_single_shard(
        dir.path(),
        vec![
            unrevealed_liquid_entry(1, 0, 0, 5),
            unrevealed_liquid_entry(2, 0, 0, 7),
        ],
    );
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(
            err,
            DatasetError::DuplicateLeafIndex {
                shard: 0,
                leaf_index: 0
            }
        ),
        "got: {err:?}"
    );
}

#[test]
fn a_leaf_index_at_or_above_the_shard_leaf_count_is_rejected() {
    let dir = tempfile::tempdir().unwrap();
    write_single_shard(
        dir.path(),
        vec![
            unrevealed_liquid_entry(1, 0, 0, 5),
            unrevealed_liquid_entry(2, 0, 5, 7),
        ],
    );
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(
            err,
            DatasetError::LeafIndexOutOfRange {
                shard: 0,
                leaf_index: 5,
                count: 2
            }
        ),
        "got: {err:?}"
    );
}

#[test]
fn a_corrupt_address_checksum_is_rejected() {
    let dir = tempfile::tempdir().unwrap();
    let mut entry = unrevealed_liquid_entry(1, 0, 0, 5);
    let mut tampered_address = entry.bech32_address.clone();
    let last = if tampered_address.pop() == Some('q') {
        'p'
    } else {
        'q'
    };
    tampered_address.push(last);
    entry.bech32_address = tampered_address;
    write_single_shard(dir.path(), vec![entry]);
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(err, DatasetError::InvalidAddress { .. }),
        "got: {err:?}"
    );
}

#[test]
fn a_duplicate_address_is_rejected() {
    let dir = tempfile::tempdir().unwrap();
    write_single_shard(
        dir.path(),
        vec![
            unrevealed_liquid_entry(1, 0, 0, 5),
            unrevealed_liquid_entry(1, 0, 1, 7),
        ],
    );
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(err, DatasetError::DuplicateAddress(_)),
        "got: {err:?}"
    );
}

#[test]
fn a_public_key_not_hashing_to_the_address_is_rejected() {
    let dir = tempfile::tempdir().unwrap();
    let mut entry = revealed_liquid_entry(3, 0, 0, 5);

    entry.bech32_address =
        distributor_core::address::encode_bech32_address(&address_bytes_in_shard(9, 0));
    write_single_shard(dir.path(), vec![entry]);
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(err, DatasetError::IdentityMismatch(_)),
        "got: {err:?}"
    );
}

#[test]
fn an_explicit_unrevealed_public_key_is_rejected() {
    let dir = tempfile::tempdir().unwrap();
    let mut entry = unrevealed_liquid_entry(1, 0, 0, 5);
    entry.public_key = Some(hex::encode([0u8; 33]));
    write_single_shard(dir.path(), vec![entry]);
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(err, DatasetError::UnrevealedPublicKeyForbidden(_)),
        "got: {err:?}"
    );
}

fn tamper_attestation(dir: &Path, mutate: impl FnOnce(&mut serde_json::Value)) {
    let path = dir.join("attestation.json");
    let mut json: serde_json::Value =
        serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
    mutate(&mut json);
    std::fs::write(&path, serde_json::to_vec(&json).unwrap()).unwrap();
}

#[test]
fn proof_cap_matches_the_max_shard_depth() {
    assert_eq!(
        1usize << distributor_dataset::MAX_PROOF_SIBLING_COUNT,
        distributor_merkle::sharded::MAX_SHARD_LEAF_COUNT
    );
}

#[test]
fn an_oversized_index_artifact_is_rejected_before_parsing() {
    let dir = tempfile::tempdir().unwrap();
    let size = usize::try_from(distributor_dataset::MAX_INDEX_FILE_BYTES).unwrap() + 1;
    std::fs::write(dir.path().join("roots.json"), vec![b'0'; size]).unwrap();
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(err, DatasetError::ArtifactTooLarge { .. }),
        "got: {err:?}"
    );
}

#[test]
fn an_overlong_proof_vector_is_rejected_before_decoding() {
    let dir = tempfile::tempdir().unwrap();
    let mut entry = unrevealed_liquid_entry(1, 0, 0, 5);
    let root = entry.leaf_hash().unwrap();
    entry.proof = vec![hex::encode([0u8; 32]); distributor_dataset::MAX_PROOF_SIBLING_COUNT + 1];
    write_dataset(dir.path(), &[root], &[1], &[(0, vec![entry])]);
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(err, DatasetError::ProofTooLong { .. }),
        "got: {err:?}"
    );
}

fn tamper_shard_file(dir: &Path, file: &str, mutate: impl FnOnce(&mut serde_json::Value)) {
    let path = dir.join(file);
    let mut json: serde_json::Value =
        serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
    mutate(&mut json);
    let bytes = serde_json::to_vec(&json).unwrap();
    std::fs::write(&path, &bytes).unwrap();
    let new_hash = keccak256(&[&bytes]);

    let manifest_path = dir.join("manifest.json");
    let mut manifest: serde_json::Value =
        serde_json::from_slice(&std::fs::read(&manifest_path).unwrap()).unwrap();
    let mut preimage: Vec<u8> = Vec::new();
    for item in manifest.as_array_mut().unwrap() {
        if item["file"] == file {
            item["file_hash"] = serde_json::json!(hex::encode(new_hash));
        }
        let sid = u16::try_from(item["shard_id"].as_u64().unwrap()).unwrap();
        let item_hash = hex::decode(item["file_hash"].as_str().unwrap()).unwrap();
        preimage.extend_from_slice(&sid.to_le_bytes());
        preimage.extend_from_slice(&item_hash);
    }
    std::fs::write(&manifest_path, serde_json::to_vec(&manifest).unwrap()).unwrap();
    tamper_attestation(dir, |attestation| {
        attestation["claim_dataset_manifest_hash"] =
            serde_json::json!(hex::encode(keccak256(&[&preimage])));
    });
}

#[test]
fn an_unknown_claim_entry_field_is_rejected() {
    let dir = tempfile::tempdir().unwrap();
    write_single_shard(dir.path(), vec![unrevealed_liquid_entry(1, 0, 0, 5)]);
    tamper_shard_file(dir.path(), "shard_0.json", |json| {
        json[0]["surprise"] = serde_json::json!(1);
    });
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(err, DatasetError::ParseFailed { .. }),
        "got: {err:?}"
    );
}

#[test]
fn shard_dataset_rejects_unknown_fields() {
    let good = r#"{"shard_id":0,"entries":[]}"#;
    assert!(serde_json::from_str::<distributor_dataset::ShardDataset>(good).is_ok());
    let bad = r#"{"shard_id":0,"entries":[],"surprise":1}"#;
    assert!(serde_json::from_str::<distributor_dataset::ShardDataset>(bad).is_err());
}

fn tamper_manifest(dir: &Path, mutate: impl FnOnce(&mut serde_json::Value)) {
    let path = dir.join("manifest.json");
    let mut json: serde_json::Value =
        serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
    mutate(&mut json);
    std::fs::write(&path, serde_json::to_vec(&json).unwrap()).unwrap();
}

#[test]
fn a_wrong_width_roots_commitment_is_a_hex_length_error() {
    let dir = tempfile::tempdir().unwrap();
    write_single_shard(dir.path(), vec![unrevealed_liquid_entry(1, 0, 0, 5)]);
    tamper_attestation(dir.path(), |attestation| {
        let mut text = attestation["roots_commitment"]
            .as_str()
            .unwrap()
            .to_string();
        text.truncate(62);
        attestation["roots_commitment"] = serde_json::json!(text);
    });
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(
            err,
            DatasetError::InvalidHexLength {
                expected: 32,
                actual: 31
            }
        ),
        "got: {err:?}"
    );
}

#[test]
fn a_tampered_total_claimable_breaks_the_commitment_binding() {
    let dir = tempfile::tempdir().unwrap();
    write_single_shard(dir.path(), vec![unrevealed_liquid_entry(1, 0, 0, 5)]);
    tamper_attestation(dir.path(), |attestation| {
        attestation["total_claimable"] = serde_json::json!("6");
    });
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(err, DatasetError::RootsCommitmentMismatch),
        "got: {err:?}"
    );
}

#[test]
fn a_total_claimable_lied_consistently_into_the_commitment_is_caught_by_the_file_recount() {
    let dir = tempfile::tempdir().unwrap();
    let mut entries = vec![unrevealed_liquid_entry(1, 0, 0, 5)];
    let root = seal_shard(&mut entries);
    write_dataset(dir.path(), &[root], &[1], &[(0, entries)]);
    tamper_attestation(dir.path(), |attestation| {
        attestation["total_claimable"] = serde_json::json!("6");
        attestation["roots_commitment"] = serde_json::json!(hex::encode(
            distributor_merkle::roots_commitment(&[root], &[1], 6)
        ));
    });
    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(
            err,
            DatasetError::TotalClaimableMismatch {
                claimed: 6,
                actual: 5
            }
        ),
        "got: {err:?}"
    );
}
