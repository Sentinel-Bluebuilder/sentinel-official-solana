use crate::CeremonyError;
use crate::keypair_file::write_new_keypair_file;
use crate::usage::{TagUsage, usage_error};
use anyhow::Context;
use distributor_core::ceremony::CeremonyAddresses;
use solana_client::rpc_client::RpcClient;
use solana_keypair::{Keypair, read_keypair_file};
use solana_pubkey::Pubkey;
use solana_signer::Signer;
use std::path::Path;

pub fn read_json<T: serde::de::DeserializeOwned>(path: &Path) -> anyhow::Result<T> {
    let bytes =
        std::fs::read(path).with_context(|| format!("failed to read {}", path.display()))?;
    serde_json::from_slice(&bytes).with_context(|| format!("failed to parse {}", path.display()))
}

pub fn shard_leaf_counts_from_manifest(dir: &Path) -> anyhow::Result<Vec<u32>> {
    let manifest: serde_json::Value = read_json(&dir.join("manifest.json"))?;
    let mut counts = vec![0u32; distributor_core::consts::SHARD_COUNT as usize];
    let mut seen = vec![false; distributor_core::consts::SHARD_COUNT as usize];
    for item in manifest.as_array().context("manifest.json: not an array")? {
        let shard_id = item["shard_id"].as_u64().context("bad shard_id")?;
        let leaf_count = item["leaf_count"].as_u64().context("bad leaf_count")?;

        let shard_id = u16::try_from(shard_id)
            .ok()
            .filter(|shard| *shard < distributor_core::consts::SHARD_COUNT)
            .with_context(|| {
                format!(
                    "manifest.json: shard_id {shard_id} >= SHARD_COUNT ({})",
                    distributor_core::consts::SHARD_COUNT
                )
            })?;

        let idx = shard_id as usize;
        #[allow(clippy::indexing_slicing)]
        let already_seen = std::mem::replace(&mut seen[idx], true);
        if already_seen {
            anyhow::bail!(
                "manifest.json: shard_id {shard_id} appears more than once; each shard must \
                 have exactly one manifest entry (its leaf count sizes that shard's bitmap \
                 and is bound into roots_commitment)"
            );
        }

        let count = u32::try_from(leaf_count)
            .ok()
            .filter(|shard_leaves| {
                *shard_leaves as usize <= distributor_merkle::sharded::MAX_SHARD_LEAF_COUNT
            })
            .with_context(|| {
                format!(
                    "manifest.json: shard {shard_id} leaf_count {leaf_count} exceeds the \
                     per-shard cap ({})",
                    distributor_merkle::sharded::MAX_SHARD_LEAF_COUNT
                )
            })?;
        #[allow(clippy::indexing_slicing)]
        {
            counts[idx] = count;
        }
    }
    Ok(counts)
}

pub fn read_roots(dir: &Path) -> anyhow::Result<Vec<[u8; 32]>> {
    let roots_hex: Vec<String> = read_json(&dir.join("roots.json"))?;
    roots_hex
        .iter()
        .map(|hex_text| {
            distributor_dataset::decode_hex_array::<32>(hex_text).map_err(anyhow::Error::from)
        })
        .collect()
}

pub fn acquire_keys_lock(keys_dir: &Path) -> anyhow::Result<std::fs::File> {
    std::fs::create_dir_all(keys_dir)
        .with_context(|| format!("failed to create {}", keys_dir.display()))
        .tag_usage()?;
    let lock_path = keys_dir.join(".ceremony.lock");
    let file = std::fs::OpenOptions::new()
        .create(true)
        .truncate(false)
        .write(true)
        .open(&lock_path)
        .with_context(|| format!("failed to open {}", lock_path.display()))
        .tag_usage()?;
    match file.try_lock() {
        Ok(()) => Ok(file),
        Err(std::fs::TryLockError::WouldBlock) => Err(usage_error(format!(
            "another ceremony process holds the lock on {} — refusing a second concurrent \
             run against the same --keys-dir (it could double-send irreversible steps)",
            lock_path.display()
        ))),
        Err(std::fs::TryLockError::Error(error)) => Err(anyhow::Error::new(error))
            .with_context(|| format!("failed to lock {}", lock_path.display())),
    }
}

pub fn check_payer_distinct(payer: &Pubkey, upgrade_authority: &Pubkey) -> anyhow::Result<()> {
    if payer == upgrade_authority {
        return Err(usage_error(format!(
            "upgrade_authority.json and payer.json name the same keypair ({payer}); the payer must \
             be distinct from the upgrade_authority, or every upgrade_authority-signed step fails with \
             SignFailed(TooManySigners)"
        )));
    }
    Ok(())
}

pub fn require_tx_v1(client: &RpcClient, payer: &Pubkey) -> anyhow::Result<()> {
    txv1::preflight::require_tx_v1(client, payer).map_err(CeremonyError::from)?;
    Ok(())
}

pub fn resolve_program_id(explicit: Option<Pubkey>, keypair_path: &Path) -> anyhow::Result<Pubkey> {
    if let Some(program_id) = explicit {
        return Ok(program_id);
    }
    read_keypair_file(keypair_path)
        .map(|keypair| keypair.pubkey())
        .map_err(|error| {
            anyhow::anyhow!(
                "failed to read program keypair {}: {error}; pass --distributor-program-id to avoid \
                 needing the program keypair on this machine",
                keypair_path.display()
            )
        })
        .tag_usage()
}

pub fn load_or_create_keypair(path: &Path, resuming: bool) -> anyhow::Result<Keypair> {
    if path.exists() {
        read_keypair_file(path)
            .map_err(|error| anyhow::anyhow!("failed to read {}: {error}", path.display()))
    } else if resuming {
        Err(usage_error(format!(
            "signatures.json shows a prior run against this --keys-dir, but {} is missing; \
             refusing to generate a fresh keypair (it would not match the identities already \
             on-chain) — restore the original file",
            path.display()
        )))
    } else {
        let keypair = Keypair::new();
        write_new_keypair_file(path, &keypair)
            .with_context(|| format!("failed to write {}", path.display()))?;
        Ok(keypair)
    }
}

pub fn log_resolved_keys(keys: &CeremonyAddresses) {
    tracing::info!(
        upgrade_authority = %keys.upgrade_authority,
        payer = %keys.payer,
        mint = %keys.mint,
        treasury = %keys.treasury,
        vault = %keys.vault,
        "resolved ceremony wiring"
    );
}

#[cfg(test)]
#[allow(
    unused_imports,
    unused_qualifications,
    clippy::unwrap_used,
    clippy::expect_used,
    clippy::panic,
    clippy::indexing_slicing,
    clippy::arithmetic_side_effects
)]
mod tests {
    use super::*;
    use crate::CeremonyError;
    use crate::exit_code;
    use crate::runner::{CeremonySigners, HaltedStep, StepRecord, run_steps};
    use crate::sender::{AccountReader, RpcSender, Sender, Transport};
    use crate::steps::{CeremonyAddresses, CeremonyInputs, RentTable, Step, build_ceremony_steps};
    use crate::usage::exit_code_for;
    use anyhow::Context as _;
    use solana_instruction::Instruction;
    use solana_keypair::Keypair;
    use solana_pubkey::Pubkey;
    use solana_signature::Signature;
    use solana_signer::Signer;
    use solana_transaction::versioned::VersionedTransaction;
    use txv1::{TxConfig, build_signed};
    fn leaf_counts_from_manifest_json(manifest: &str) -> anyhow::Result<Vec<u32>> {
        let dir = tempfile::tempdir().expect("tempdir");
        std::fs::write(dir.path().join("manifest.json"), manifest).expect("write manifest");
        shard_leaf_counts_from_manifest(dir.path())
    }

    #[test]
    fn manifest_leaf_counts_are_shard_indexed_with_zeros_for_absent_shards() {
        let counts = leaf_counts_from_manifest_json(
            r#"[{"shard_id":3,"leaf_count":7,"file_hash":"aa","file":"a.json"},
                {"shard_id":0,"leaf_count":1,"file_hash":"bb","file":"b.json"}]"#,
        )
        .expect("well-formed manifest");
        assert_eq!(counts.len(), distributor_core::consts::SHARD_COUNT as usize);
        assert_eq!(counts[0], 1);
        assert_eq!(counts[3], 7);
        assert!(
            counts
                .iter()
                .enumerate()
                .all(
                    |(shard_id, &leaf_count)| matches!((shard_id, leaf_count), (0, 1) | (3, 7))
                        || leaf_count == 0
                )
        );
    }

    #[test]
    fn manifest_rejects_a_repeated_shard_id() {
        let err = leaf_counts_from_manifest_json(
            r#"[{"shard_id":5,"leaf_count":7,"file_hash":"aa","file":"a.json"},
                {"shard_id":5,"leaf_count":9,"file_hash":"bb","file":"b.json"}]"#,
        )
        .expect_err("duplicate shard_id must be refused")
        .to_string();
        assert!(err.contains("more than once"), "got: {err}");
        assert!(err.contains("shard_id 5"), "got: {err}");
    }

    #[test]
    fn manifest_rejects_out_of_range_shard_id_and_oversized_leaf_count() {
        let err = leaf_counts_from_manifest_json(r#"[{"shard_id":128,"leaf_count":1}]"#)
            .expect_err("shard_id >= SHARD_COUNT must be refused")
            .to_string();
        assert!(err.contains(">= SHARD_COUNT"), "got: {err}");

        let big = distributor_merkle::sharded::MAX_SHARD_LEAF_COUNT + 1;
        let err =
            leaf_counts_from_manifest_json(&format!(r#"[{{"shard_id":1,"leaf_count":{big}}}]"#))
                .expect_err("leaf_count past the cap must be refused")
                .to_string();
        assert!(err.contains("exceeds the per-shard cap"), "got: {err}");

        let err = leaf_counts_from_manifest_json(r#"[{"shard_id":1,"leaf_count":4294967296}]"#)
            .expect_err("leaf_count past u32 must be refused, not truncated")
            .to_string();
        assert!(err.contains("exceeds the per-shard cap"), "got: {err}");
    }

    #[test]
    fn keys_lock_refuses_a_second_holder_and_releases_on_drop() {
        let dir = tempfile::tempdir().expect("tempdir");
        let first = acquire_keys_lock(dir.path()).expect("first lock");
        let err = acquire_keys_lock(dir.path()).expect_err("second holder must refuse");
        assert_eq!(exit_code_for(&err), exit_code::USAGE);
        assert!(
            format!("{err}").contains("another ceremony process"),
            "got: {err}"
        );
        drop(first);
        acquire_keys_lock(dir.path()).expect("released lock can be re-acquired");
    }

    #[test]
    fn payer_equal_to_upgrade_authority_is_refused_up_front() {
        let shared = Pubkey::new_unique();
        let err = check_payer_distinct(&shared, &shared).expect_err("a shared keypair must refuse");
        assert_eq!(exit_code_for(&err), exit_code::USAGE);
        assert!(format!("{err}").contains("distinct"), "got: {err}");
        check_payer_distinct(&Pubkey::new_unique(), &Pubkey::new_unique())
            .expect("distinct keys pass");
    }

    #[test]
    fn resume_with_signatures_but_missing_keypair_is_refused_not_regenerated() {
        let dir = tempfile::tempdir().expect("tempdir");
        let payer_path = dir.path().join("payer.json");

        let err = load_or_create_keypair(&payer_path, true)
            .expect_err("resuming with a missing keypair must refuse");
        assert_eq!(exit_code_for(&err), exit_code::USAGE);
        assert!(
            format!("{err}").contains("refusing to generate"),
            "got: {err}"
        );
        assert!(
            !payer_path.exists(),
            "no keypair may be fabricated on the refusal path"
        );

        let created = load_or_create_keypair(&payer_path, false).expect("fresh run creates");
        let reloaded = load_or_create_keypair(&payer_path, true)
            .expect("an existing file loads fine while resuming");
        assert_eq!(created.pubkey(), reloaded.pubkey());
    }

    #[test]
    fn resolve_program_id_prefers_the_explicit_id_over_the_keypair_file() {
        let explicit = Pubkey::new_unique();
        let missing = Path::new("/nonexistent-ceremony-test/kp.json");
        assert_eq!(
            resolve_program_id(Some(explicit), missing).expect("explicit id needs no file"),
            explicit
        );
        let err = resolve_program_id(None, missing).expect_err("no id and no file must refuse");
        assert_eq!(exit_code_for(&err), exit_code::USAGE);
        assert!(
            format!("{err}").contains("--distributor-program-id"),
            "got: {err}"
        );

        let dir = tempfile::tempdir().expect("tempdir");
        let path = dir.path().join("kp.json");
        let keypair = Keypair::new();
        crate::keypair_file::write_new_keypair_file(&path, &keypair).expect("write keypair");
        assert_eq!(
            resolve_program_id(None, &path).expect("fallback reads the file"),
            keypair.pubkey()
        );
    }

    #[test]
    fn preflight_failure_halts_before_any_submit() {
        fn guarded_send(
            client: &RpcClient,
            payer: &Pubkey,
            submitted: &mut bool,
        ) -> anyhow::Result<()> {
            require_tx_v1(client, payer)?;
            *submitted = true;
            Ok(())
        }

        let client = RpcClient::new_mock("succeeds");
        let mut submitted = false;
        let err = guarded_send(&client, &Pubkey::new_unique(), &mut submitted)
            .expect_err("an inactive enable_tx_v1 feature must refuse");

        assert!(
            !submitted,
            "preflight failure must halt before the send runs"
        );
        let ceremony_error = err
            .downcast_ref::<CeremonyError>()
            .expect("must surface as a CeremonyError, not a bare PreflightError");
        assert!(
            matches!(
                ceremony_error,
                CeremonyError::PreflightFeatureInactive { .. }
            ),
            "got {ceremony_error:?}"
        );
        assert!(
            !ceremony_error.is_transient(),
            "a v1-incapable cluster must halt, never retry as transient"
        );
        assert_eq!(exit_code_for(&err), exit_code::GATE_REFUSED);
    }
}
