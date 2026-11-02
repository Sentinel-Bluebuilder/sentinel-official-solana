use xtask::build_tree::{build, write_output};
use xtask::ledger::ParsedRecord;

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

fn trailing_content_hash(recs: &[ParsedRecord]) -> [u8; 32] {
    let bytes = xtask::ledger::encode_ledger(recs);
    bytes[bytes.len() - 32..].try_into().unwrap()
}

fn valid_dataset() -> tempfile::TempDir {
    let recs = vec![revealed(1, 100), revealed(2, 200)];
    let out = build(&recs, trailing_content_hash(&recs)).unwrap();
    let dir = tempfile::tempdir().unwrap();
    write_output(dir.path(), &out).unwrap();
    dir
}

fn read_json(path: &std::path::Path) -> serde_json::Value {
    serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap()
}

fn write_json(path: &std::path::Path, value: &serde_json::Value) {
    std::fs::write(path, serde_json::to_vec_pretty(value).unwrap()).unwrap();
}

#[test]
fn accepts_a_freshly_built_dataset() {
    let dir = valid_dataset();
    distributor_dataset::require_valid_dataset(dir.path()).unwrap();
}

#[test]
fn rejects_a_tampered_total_claimable() {
    let dir = valid_dataset();
    let att_path = dir.path().join("attestation.json");
    let mut att = read_json(&att_path);
    att["total_claimable"] = serde_json::Value::String("999999".into());
    write_json(&att_path, &att);
    let err = distributor_dataset::require_valid_dataset(dir.path())
        .unwrap_err()
        .to_string();
    assert!(
        err.contains("roots_commitment"),
        "expected the tampered total to break the binding check: {err}"
    );
}

#[test]
fn rejects_a_tampered_shard_leaf_count() {
    let dir = valid_dataset();
    let att_path = dir.path().join("attestation.json");
    let mut att = read_json(&att_path);
    let shard = att["shard_leaf_counts"]
        .as_array()
        .unwrap()
        .iter()
        .position(|count| count.as_u64() == Some(1))
        .expect("a shard with exactly one leaf");
    att["shard_leaf_counts"][shard] = serde_json::json!(0);
    write_json(&att_path, &att);
    let err = distributor_dataset::require_valid_dataset(dir.path())
        .unwrap_err()
        .to_string();
    assert!(
        err.contains("roots_commitment") || err.contains("shard_leaf_counts"),
        "expected the tampered counts to break a binding check: {err}"
    );
}

#[test]
fn rejects_a_shard_leaf_count_past_the_per_shard_cap_before_any_transaction() {
    let dir = valid_dataset();
    let att_path = dir.path().join("attestation.json");
    let mut att = read_json(&att_path);
    let over = distributor_merkle::sharded::MAX_SHARD_LEAF_COUNT + 1;
    att["shard_leaf_counts"][0] = serde_json::json!(over);
    write_json(&att_path, &att);
    let err = distributor_dataset::require_valid_dataset(dir.path())
        .unwrap_err()
        .to_string();
    assert!(
        err.contains("per-shard cap") && err.contains(&over.to_string()),
        "expected the cap preflight to name the offending count: {err}"
    );
}

#[test]
fn rejects_manifest_path_that_escapes_the_dataset_dir_via_parent_dir() {
    let dir = valid_dataset();
    let outside = tempfile::tempdir().unwrap();
    std::fs::write(outside.path().join("evil.json"), b"[]").unwrap();
    let manifest_path = dir.path().join("manifest.json");
    let mut manifest = read_json(&manifest_path);
    let escaped = format!(
        "../{}/evil.json",
        outside.path().file_name().unwrap().to_str().unwrap()
    );
    manifest[0]["file"] = serde_json::Value::String(escaped);
    manifest[0]["leaf_count"] = serde_json::json!(0);
    manifest[0]["file_hash"] =
        serde_json::Value::String(hex::encode(distributor_core::hash::keccak256(&[b"[]"])));
    write_json(&manifest_path, &manifest);

    let err = distributor_dataset::require_valid_dataset(dir.path())
        .unwrap_err()
        .to_string();
    assert!(
        err.to_lowercase().contains("escape") || err.to_lowercase().contains("path"),
        "expected a path-confinement error, got: {err}"
    );
}

fn flip_one_byte(path: &std::path::Path) {
    let mut bytes = std::fs::read(path).unwrap();
    let first = bytes.first_mut().unwrap();
    *first ^= 0xff;
    std::fs::write(path, bytes).unwrap();
}

#[test]
fn require_valid_dataset_fails_on_a_flipped_shard_byte() {
    let dir = valid_dataset();
    let shard_file = std::fs::read_dir(dir.path().join("claim_dataset"))
        .unwrap()
        .next()
        .unwrap()
        .unwrap()
        .path();
    flip_one_byte(&shard_file);
    let err = distributor_dataset::require_valid_dataset(dir.path())
        .unwrap_err()
        .to_string();
    assert!(
        err.contains("shard file hash mismatch"),
        "expected a ShardHashMismatch: {err}"
    );
}
