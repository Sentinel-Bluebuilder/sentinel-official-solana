use distributor_vesting::schedule::{Period, Schedule, ScheduleKind};
use xtask::build_tree::{build, write_output};
use xtask::ledger::{LedgerError, ParsedRecord, decode_ledger, encode_ledger};

fn compressed_public_key(seed: u8) -> [u8; 33] {
    use k256::elliptic_curve::sec1::ToSec1Point;
    let signing_key = k256::SecretKey::from_slice(&[seed.max(1); 32]).unwrap();
    let mut out = [0u8; 33];
    out.copy_from_slice(signing_key.public_key().to_sec1_point(true).as_bytes());
    out
}

fn revealed(seed: u8, liquid: u64) -> ParsedRecord {
    let public_key = compressed_public_key(seed);
    ParsedRecord {
        bech32_address: distributor_core::address::encode_bech32_address(
            &distributor_core::hash::hash160(&public_key),
        ),
        compressed_public_key: Some(public_key),
        liquid_amount: liquid,
        locked_amount: 0,
        schedule: None,
    }
}

fn locked_record(seed: u8, liquid: u64, locked: u64, schedule: Schedule) -> ParsedRecord {
    let public_key = compressed_public_key(seed);
    ParsedRecord {
        bech32_address: distributor_core::address::encode_bech32_address(
            &distributor_core::hash::hash160(&public_key),
        ),
        compressed_public_key: Some(public_key),
        liquid_amount: liquid,
        locked_amount: locked,
        schedule: Some(schedule),
    }
}

#[test]
fn ledger_round_trips_and_content_hash_is_checked() {
    let recs = vec![revealed(1, 100), revealed(2, 200)];
    let bytes = encode_ledger(&recs);
    assert_eq!(decode_ledger(&bytes).unwrap().len(), 2);
    let mut bad = bytes.clone();
    bad[30] ^= 0xff;
    assert!(matches!(
        decode_ledger(&bad),
        Err(LedgerError::ContentHashMismatch)
    ));
}

#[test]
fn parse_rejects_zero_entitlement_record() {
    let recs = vec![revealed(1, 100), revealed(2, 0)];
    let bytes = encode_ledger(&recs);
    assert!(matches!(
        decode_ledger(&bytes),
        Err(LedgerError::ZeroEntitlement)
    ));
}

fn trailing_content_hash(recs: &[ParsedRecord]) -> [u8; 32] {
    let bytes = encode_ledger(recs);
    bytes[bytes.len() - 32..].try_into().unwrap()
}

#[test]
fn build_tree_proofs_and_commitment_are_consistent() {
    let recs = vec![revealed(1, 100), revealed(2, 200)];
    let out = build(&recs, trailing_content_hash(&recs)).unwrap();
    assert_eq!(out.attestation.schema_version, 3);
    assert_eq!(
        out.attestation.shard_leaf_counts.len(),
        distributor_core::consts::SHARD_COUNT as usize
    );
    assert_eq!(out.attestation.shard_leaf_counts.iter().sum::<u32>(), 2);
    assert_eq!(
        out.roots_commitment,
        distributor_merkle::roots_commitment(
            &out.roots,
            &out.attestation.shard_leaf_counts,
            out.attestation.total_claimable
        )
    );
    assert_eq!(
        out.attestation.roots_commitment,
        hex::encode(out.roots_commitment)
    );
    let sum: u64 = out.attestation.total_claimable;
    assert_eq!(sum, 300);
    for shard in &out.shards {
        for entry in &shard.entries {
            let proof: Vec<[u8; 32]> = entry
                .proof
                .iter()
                .map(|hex_text| {
                    let mut proof_hash = [0u8; 32];
                    proof_hash.copy_from_slice(&hex::decode(hex_text).unwrap());
                    proof_hash
                })
                .collect();
            assert!(distributor_merkle::tree::is_valid_proof(
                &entry.leaf_hash().unwrap(),
                &proof,
                &out.roots[entry.shard_id as usize]
            ));
        }
    }
}

#[test]
fn attestation_ledger_hash_equals_the_embedded_trailing_hash() {
    let recs = vec![revealed(1, 100), revealed(2, 200)];
    let bytes = encode_ledger(&recs);
    let trailing = &bytes[bytes.len() - 32..];
    let parsed = decode_ledger(&bytes).unwrap();
    let out = build(&parsed, trailing.try_into().unwrap()).unwrap();
    assert_eq!(out.attestation.ledger_content_hash, hex::encode(trailing));
    assert_ne!(
        out.attestation.ledger_content_hash,
        hex::encode(distributor_core::hash::keccak256(&[&bytes]))
    );
}

#[test]
fn build_rejects_schedule_over_512_periods() {
    let periods: Vec<Period> = (0..513)
        .map(|_| Period {
            length_seconds: 1,
            amount: 1,
        })
        .collect();
    let schedule = Schedule {
        kind: ScheduleKind::Periodic,
        start_at: 0,
        end_at: 513,
        periods,
        total: 513,
    };
    let recs = [locked_record(3, 0, 513, schedule)];
    let err = build(&recs, trailing_content_hash(&recs)).unwrap_err();
    assert!(format!("{err:?}").contains("InvalidSchedule"));
}

#[test]
fn build_rejects_schedule_total_mismatch() {
    let schedule = Schedule {
        kind: ScheduleKind::Continuous,
        start_at: 0,
        end_at: 10,
        periods: vec![],
        total: 999,
    };
    let recs = [locked_record(4, 0, 500, schedule)];
    let err = build(&recs, trailing_content_hash(&recs)).unwrap_err();
    assert!(format!("{err:?}").contains("InvalidSchedule"));
}

#[test]
fn write_output_refuses_a_nonempty_claim_dataset_dir() {
    let recs = vec![revealed(1, 100), revealed(2, 200)];
    let out = build(&recs, trailing_content_hash(&recs)).unwrap();
    let dir = tempfile::tempdir().unwrap();
    std::fs::create_dir_all(dir.path().join("claim_dataset")).unwrap();
    std::fs::write(
        dir.path().join("claim_dataset").join("shard_9999.json"),
        b"[]",
    )
    .unwrap();

    let err = write_output(dir.path(), &out).unwrap_err();
    assert_eq!(err.kind(), std::io::ErrorKind::AlreadyExists);
    assert!(
        dir.path()
            .join("claim_dataset")
            .join("shard_9999.json")
            .exists(),
        "a stale file must be left in place for the operator to inspect, not silently deleted"
    );
    assert!(
        !dir.path().join("attestation.json").exists(),
        "no top-level output should be written once the guard refuses"
    );
}

#[test]
fn manifest_hash_matches_bytes_written_by_write_output() {
    let recs = vec![revealed(1, 100), revealed(2, 200)];
    let out = build(&recs, trailing_content_hash(&recs)).unwrap();
    let dir = tempfile::tempdir().unwrap();
    write_output(dir.path(), &out).unwrap();

    let mut acc = Vec::new();
    for shard in &out.shards {
        let bytes = std::fs::read(
            dir.path()
                .join("claim_dataset")
                .join(format!("shard_{}.json", shard.shard_id)),
        )
        .unwrap();
        acc.extend_from_slice(&shard.shard_id.to_le_bytes());
        acc.extend_from_slice(&distributor_core::hash::keccak256(&[&bytes]));
    }
    let recomputed = distributor_core::hash::keccak256(&[&acc]);
    assert_eq!(
        distributor_dataset::manifest_hash_of(&out.shards),
        recomputed
    );
    assert_eq!(
        out.attestation.claim_dataset_manifest_hash,
        hex::encode(recomputed)
    );
}

#[test]
fn write_output_succeeds_when_claim_dataset_dir_is_absent_or_empty() {
    let recs = vec![revealed(1, 100), revealed(2, 200)];
    let out = build(&recs, trailing_content_hash(&recs)).unwrap();

    let dir = tempfile::tempdir().unwrap();
    write_output(dir.path(), &out).unwrap();
    assert!(dir.path().join("attestation.json").exists());

    let dir2 = tempfile::tempdir().unwrap();
    std::fs::create_dir_all(dir2.path().join("claim_dataset")).unwrap();
    write_output(dir2.path(), &out).unwrap();
    assert!(dir2.path().join("attestation.json").exists());
}
