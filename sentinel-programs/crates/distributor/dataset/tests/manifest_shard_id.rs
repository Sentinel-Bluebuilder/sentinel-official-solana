use distributor_dataset::DatasetError;

fn write_minimal_valid_index(dir: &std::path::Path) {
    let roots: Vec<[u8; 32]> = vec![[0u8; 32]];
    let shard_leaf_counts: Vec<u32> = vec![0];
    let roots_commitment = distributor_merkle::roots_commitment(&roots, &shard_leaf_counts, 0);

    let roots_json: Vec<String> = roots.iter().map(hex::encode).collect();
    std::fs::write(
        dir.join("roots.json"),
        serde_json::to_vec(&roots_json).unwrap(),
    )
    .unwrap();

    let attestation = serde_json::json!({
        "schema_version": 3,
        "roots_commitment": hex::encode(roots_commitment),
        "shard_leaf_counts": shard_leaf_counts,
        "total_claimable": "0",
        "leaf_count": 0,
        "shard_count": 1,
        "ledger_content_hash": hex::encode([0u8; 32]),
        "claim_dataset_manifest_hash": hex::encode([0u8; 32]),
        "git_commit": "g",
        "tool_version": "v",
    });
    std::fs::write(
        dir.join("attestation.json"),
        serde_json::to_vec(&attestation).unwrap(),
    )
    .unwrap();
}

#[test]
fn manifest_shard_id_out_of_u16_range_is_rejected_not_truncated() {
    let dir = tempfile::tempdir().unwrap();
    write_minimal_valid_index(dir.path());

    let manifest = serde_json::json!([
        {"file": "shard0.json", "shard_id": 65541, "file_hash": "00", "leaf_count": 0}
    ]);
    std::fs::write(
        dir.path().join("manifest.json"),
        serde_json::to_vec(&manifest).unwrap(),
    )
    .unwrap();

    let err = distributor_dataset::require_valid_dataset(dir.path()).unwrap_err();
    assert!(
        matches!(err, DatasetError::ParseFailed { .. }),
        "expected a typed manifest-parse rejection, got: {err:?}"
    );
}
