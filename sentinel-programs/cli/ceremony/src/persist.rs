use crate::runner::StepRecord;
use crate::usage::usage_error;
use anchor_lang::AccountDeserialize;
use anyhow::Context;
use distributor_core::ceremony::CeremonyAddresses;
use solana_client::rpc_client::RpcClient;
use solana_commitment_config::CommitmentConfig;
use solana_pubkey::Pubkey;
use solana_signature::Signature;
use std::path::Path;

pub fn check_resumed_config(
    config_data: Option<&[u8]>,
    keys: &CeremonyAddresses,
) -> anyhow::Result<()> {
    let Some(mut data) = config_data else {
        return Ok(());
    };
    let config = distributor::state::Config::try_deserialize(&mut data)
        .context("config PDA exists but its account data does not deserialize as Config")?;
    let mut mismatches = Vec::new();
    for (field, onchain, loaded) in [
        (
            "upgrade_authority",
            config.upgrade_authority,
            keys.upgrade_authority,
        ),
        ("mint", config.mint, keys.mint),
        ("treasury", config.treasury, keys.treasury),
        ("vault", config.vault, keys.vault),
    ] {
        if onchain != loaded {
            mismatches.push(format!("{field}: on-chain {onchain} vs loaded {loaded}"));
        }
    }
    if !mismatches.is_empty() {
        anyhow::bail!(
            "refusing to resume: the on-chain Config was initialized with different \
             identities than --keys-dir now loads ({}); this looks like another ceremony's \
             state — do not continue with these keypairs",
            mismatches.join("; ")
        );
    }
    Ok(())
}

pub fn check_finalize_genesis_not_in_flight(
    prior_records: &[StepRecord],
    config_present: bool,
) -> anyhow::Result<()> {
    let finalize_genesis_on_record = prior_records.iter().any(|record| {
        record.step == "distributor_finalize_genesis"
            && (record.signature.is_some() || record.skipped)
    });
    if finalize_genesis_on_record && !config_present {
        anyhow::bail!(
            "signatures.json records a finalize genesis for this --keys-dir, but the Config PDA is \
             not observable at finalized commitment; the finalize transaction may still be \
             in flight (or --rpc-url points at the wrong cluster). Refusing to treat this as a \
             fresh run — wait for finalization and re-run, and check the recorded finalize genesis \
             signature's status before anything else"
        );
    }
    Ok(())
}

pub fn require_resumed_config_matches(
    client: &RpcClient,
    program_id: &Pubkey,
    keys: &CeremonyAddresses,
    prior_records: &[StepRecord],
) -> anyhow::Result<()> {
    let (config_pda, _) = distributor_core::pda::find_config(program_id);
    let config_data = client
        .get_account_with_commitment(&config_pda, CommitmentConfig::finalized())
        .context("failed to fetch Config for the resume consistency check")?
        .value
        .map(|account| account.data);
    check_finalize_genesis_not_in_flight(prior_records, config_data.is_some())?;
    check_resumed_config(config_data.as_deref(), keys)
}

pub fn step_record_json(record: &StepRecord) -> serde_json::Value {
    serde_json::json!({
        "step": record.step,
        "signature": record.signature.map(|signature| signature.to_string()),
        "slot": record.slot,
        "skipped": record.skipped,
    })
}

pub fn enrich_slots(client: &RpcClient, records: &mut [StepRecord]) {
    let landed: Vec<(usize, Signature)> = records
        .iter()
        .enumerate()
        .filter_map(|(index, record)| record.signature.map(|sig| (index, sig)))
        .collect();
    for chunk in landed.chunks(256) {
        let sigs: Vec<Signature> = chunk.iter().map(|&(_, sig)| sig).collect();
        let Ok(resp) = client.get_signature_statuses(&sigs) else {
            continue;
        };
        for (&(index, _), status) in chunk.iter().zip(resp.value) {
            if let Some(record) = records.get_mut(index) {
                record.slot = status.map(|confirmed| confirmed.slot);
            }
        }
    }
}

pub fn emit_and_persist(records: &[StepRecord], json: bool, path: &Path) -> anyhow::Result<()> {
    if json {
        for record in records {
            println!("{}", step_record_json(record));
        }
    }
    let merged = merge_prior_records(records, path);
    let values: Vec<_> = merged.iter().map(step_record_json).collect();
    write_atomic(path, &serde_json::to_vec_pretty(&values)?)
}

pub fn persist_best_effort(
    client: &RpcClient,
    records: &mut [StepRecord],
    json: bool,
    path: &Path,
) {
    enrich_slots(client, records);
    if let Err(error) = emit_and_persist(records, json, path) {
        eprintln!("warning: failed to persist step signatures after a halted run: {error}");
    }
}

pub fn parse_step_records_lenient(values: Vec<serde_json::Value>) -> Vec<StepRecord> {
    values
        .into_iter()
        .filter_map(|value| {
            let step = value.get("step")?.as_str()?.to_string();
            let signature = value
                .get("signature")
                .and_then(serde_json::Value::as_str)
                .and_then(|text| text.parse::<Signature>().ok());
            let slot = value.get("slot").and_then(serde_json::Value::as_u64);
            let skipped = value
                .get("skipped")
                .and_then(serde_json::Value::as_bool)
                .unwrap_or(false);
            Some(StepRecord {
                step,
                signature,
                slot,
                skipped,
            })
        })
        .collect()
}

pub fn read_prior_records_lenient(path: &Path) -> Vec<StepRecord> {
    let Ok(raw) = std::fs::read_to_string(path) else {
        return Vec::new();
    };
    let Ok(values) = serde_json::from_str::<Vec<serde_json::Value>>(&raw) else {
        eprintln!(
            "warning: {} exists but is not valid JSON; not merging its prior signatures",
            path.display()
        );
        return Vec::new();
    };
    parse_step_records_lenient(values)
}

#[derive(serde::Deserialize)]
#[serde(deny_unknown_fields)]
struct RawStepRecord {
    step: String,
    signature: Option<String>,
    slot: Option<u64>,
    skipped: bool,
}

pub fn parse_step_records_strict(
    values: Vec<serde_json::Value>,
) -> Result<Vec<StepRecord>, String> {
    values
        .into_iter()
        .enumerate()
        .map(|(index, value)| {
            let raw: RawStepRecord =
                serde_json::from_value(value).map_err(|error| format!("record {index}: {error}"))?;
            let signature = raw
                .signature
                .map(|text| {
                    text.parse::<Signature>()
                        .map_err(|error| format!("record {index} (step {:?}): signature {text:?} does not parse as a real signature: {error}", raw.step))
                })
                .transpose()?;
            Ok(StepRecord {
                step: raw.step,
                signature,
                slot: raw.slot,
                skipped: raw.skipped,
            })
        })
        .collect()
}

pub fn read_prior_records_strict(path: &Path) -> anyhow::Result<Vec<StepRecord>> {
    let raw = match std::fs::read_to_string(path) {
        Ok(raw) => raw,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(Vec::new()),
        Err(error) => {
            return Err(usage_error(format!(
                "failed to read {} for the resume consistency check: {error}",
                path.display()
            )));
        }
    };
    let values: Vec<serde_json::Value> = serde_json::from_str(&raw).map_err(|error| {
        usage_error(format!(
            "{} exists but is not valid JSON ({error}); refusing to resume without knowing \
                 whether a finalize genesis transaction was already sent (a corrupt file could hide \
                 an in-flight finalize genesis) — restore it from backup, or if you have independently \
                 confirmed on-chain that no ceremony step has landed yet, remove it to start \
                 fresh",
            path.display()
        ))
    })?;
    parse_step_records_strict(values).map_err(|error| {
        usage_error(format!(
            "{} exists but contains a malformed record ({error}); refusing to resume without \
             knowing whether a finalize genesis transaction was already sent (a malformed record could \
             be hiding it) — restore the file from backup, or if you have independently \
             confirmed on-chain that no ceremony step has landed yet, remove it to start fresh",
            path.display()
        ))
    })
}

pub fn merge_prior_records(records: &[StepRecord], path: &Path) -> Vec<StepRecord> {
    let prior = read_prior_records_lenient(path);
    let mut seen: std::collections::HashSet<&str> =
        std::collections::HashSet::with_capacity(records.len());
    let mut merged: Vec<StepRecord> = records
        .iter()
        .map(|incoming| {
            seen.insert(incoming.step.as_str());
            let carried = prior.iter().find(|prior_record| {
                prior_record.step == incoming.step && prior_record.signature.is_some()
            });
            match (incoming.signature, carried) {
                (Some(sig), Some(prior_record)) if prior_record.signature == Some(sig) => {
                    StepRecord {
                        step: incoming.step.clone(),
                        signature: incoming.signature,
                        slot: incoming.slot.or(prior_record.slot),
                        skipped: incoming.skipped,
                    }
                }
                (Some(_), Some(prior_record))
                    if incoming.slot.is_none() && prior_record.slot.is_some() =>
                {
                    eprintln!(
                        "warning: step {} was re-sent as {:?} but never confirmed; keeping \
                         the prior landed signature {:?} (slot {:?})",
                        incoming.step,
                        incoming.signature,
                        prior_record.signature,
                        prior_record.slot
                    );
                    StepRecord {
                        step: incoming.step.clone(),
                        signature: prior_record.signature,
                        slot: prior_record.slot,
                        skipped: incoming.skipped,
                    }
                }
                (Some(_), _) => StepRecord {
                    step: incoming.step.clone(),
                    signature: incoming.signature,
                    slot: incoming.slot,
                    skipped: incoming.skipped,
                },
                (None, Some(prior_record)) => StepRecord {
                    step: incoming.step.clone(),
                    signature: prior_record.signature,
                    slot: prior_record.slot.or(incoming.slot),
                    skipped: incoming.skipped,
                },
                (None, None) => incoming.clone(),
            }
        })
        .collect();
    merged.extend(
        prior
            .into_iter()
            .filter(|prior_record| !seen.contains(prior_record.step.as_str())),
    );
    merged
}

pub fn write_atomic(path: &Path, bytes: &[u8]) -> anyhow::Result<()> {
    let tmp_path = path.with_extension("json.tmp");
    crate::runner::write_file_fsync(&tmp_path, bytes)?;
    std::fs::rename(&tmp_path, path).with_context(|| {
        format!(
            "failed to rename {} to {}",
            tmp_path.display(),
            path.display()
        )
    })?;
    let parent = path
        .parent()
        .filter(|path| !path.as_os_str().is_empty())
        .unwrap_or_else(|| Path::new("."));
    let dir = std::fs::File::open(parent)
        .with_context(|| format!("failed to open {} to fsync the rename", parent.display()))?;
    dir.sync_all()
        .with_context(|| format!("failed to fsync {}", parent.display()))
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
    use std::sync::Arc;
    use txv1::{TxConfig, build_signed};
    fn test_keys() -> CeremonyAddresses {
        CeremonyAddresses {
            upgrade_authority: Pubkey::new_unique(),
            payer: Pubkey::new_unique(),
            mint: Pubkey::new_unique(),
            treasury: Pubkey::new_unique(),
            vault: Pubkey::new_unique(),
        }
    }

    fn serialized_config(keys: &CeremonyAddresses) -> Vec<u8> {
        use anchor_lang::AccountSerialize as _;
        let config = distributor::state::Config {
            upgrade_authority: keys.upgrade_authority,
            mint: keys.mint,
            treasury: keys.treasury,
            vault: keys.vault,
            vault_authority_bump: 0,
            roots_commitment: [0u8; 32],
            total_claimable: 0,
            bitmap_count: 0,
            finalized: false,
            leaf_counts: [0u32; distributor_core::consts::SHARD_COUNT as usize],
            bump: 0,
            token_program: Pubkey::new_unique(),
        };
        let mut bytes = Vec::new();
        config.try_serialize(&mut bytes).expect("serialize Config");
        bytes
    }

    fn step_record(step: &str, signature: Option<Signature>, skipped: bool) -> StepRecord {
        StepRecord {
            step: step.into(),
            signature,
            slot: None,
            skipped,
        }
    }

    #[test]
    fn step_record_json_matches_the_documented_shape() {
        let sig = Signature::new_unique();
        let landed = step_record_json(&StepRecord {
            step: "distributor_create_mint".into(),
            signature: Some(sig),
            slot: Some(42),
            skipped: false,
        });
        assert_eq!(
            landed,
            serde_json::json!({
                "step": "distributor_create_mint",
                "signature": sig.to_string(),
                "slot": 42,
                "skipped": false,
            })
        );

        let skipped = step_record_json(&StepRecord {
            step: "distributor_initialize_roots".into(),
            signature: None,
            slot: None,
            skipped: true,
        });
        assert_eq!(
            skipped,
            serde_json::json!({
                "step": "distributor_initialize_roots",
                "signature": null,
                "slot": null,
                "skipped": true,
            })
        );
    }

    #[test]
    fn emit_and_persist_writes_every_record_to_the_signatures_file() {
        let dir = tempfile::tempdir().expect("tempdir");
        let path = dir.path().join("signatures.json");
        let sig = Signature::new_unique();
        let records = vec![
            StepRecord {
                step: "distributor_create_mint".into(),
                signature: Some(sig),
                slot: Some(7),
                skipped: false,
            },
            StepRecord {
                step: "distributor_initialize_roots".into(),
                signature: None,
                slot: None,
                skipped: true,
            },
        ];
        emit_and_persist(&records, false, &path).expect("persist should not require --json");
        let written: serde_json::Value =
            serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        assert_eq!(
            written,
            serde_json::json!([
                {"step": "distributor_create_mint", "signature": sig.to_string(), "slot": 7, "skipped": false},
                {"step": "distributor_initialize_roots", "signature": null, "slot": null, "skipped": true},
            ])
        );
    }

    #[test]
    fn merge_never_replaces_a_confirmed_prior_signature_with_an_unconfirmed_resend() {
        let dir = tempfile::tempdir().expect("tempdir");
        let path = dir.path().join("signatures.json");
        let landed = Signature::new_unique();
        let resent = Signature::new_unique();
        let prior = [StepRecord {
            step: "distributor_initialize_config".into(),
            signature: Some(landed),
            slot: Some(5),
            skipped: false,
        }];
        let values: Vec<_> = prior.iter().map(step_record_json).collect();
        std::fs::write(&path, serde_json::to_vec_pretty(&values).unwrap()).unwrap();

        let this_run = [StepRecord {
            step: "distributor_initialize_config".into(),
            signature: Some(resent),
            slot: None,
            skipped: false,
        }];
        let merged = merge_prior_records(&this_run, &path);
        assert_eq!(merged.len(), 1);
        assert_eq!(
            merged[0].signature,
            Some(landed),
            "the confirmed prior signature must survive an unconfirmed resend"
        );
        assert_eq!(merged[0].slot, Some(5));

        let confirmed_resend = [StepRecord {
            step: "distributor_initialize_config".into(),
            signature: Some(resent),
            slot: Some(9),
            skipped: false,
        }];
        let merged = merge_prior_records(&confirmed_resend, &path);
        assert_eq!(merged[0].signature, Some(resent));
        assert_eq!(merged[0].slot, Some(9));

        let same_sig = [StepRecord {
            step: "distributor_initialize_config".into(),
            signature: Some(landed),
            slot: None,
            skipped: false,
        }];
        let merged = merge_prior_records(&same_sig, &path);
        assert_eq!(merged[0].signature, Some(landed));
        assert_eq!(
            merged[0].slot,
            Some(5),
            "a known slot must not be forgotten"
        );
    }

    #[test]
    fn a_confirm_timeout_still_persists_the_halted_steps_signature_to_disk() {
        struct FakeSender {
            calls: usize,
        }
        impl Sender for FakeSender {
            fn send(
                &mut self,
                step_name: &str,
                _ixs: &[Instruction],
                _signers: &[&Keypair],
            ) -> Result<Signature, CeremonyError> {
                self.calls += 1;
                if step_name == "b" {
                    return Err(CeremonyError::TransportFailed {
                        signature: Signature::new_unique(),
                        message: "confirmation timed out".into(),
                    });
                }
                Ok(Signature::new_unique())
            }
            fn is_step_done(&self, _step: &Step) -> bool {
                false
            }
        }

        let steps = vec![
            crate::steps::Step {
                name: "a".into(),
                ixs: vec![],
                extra_signers: vec![],
                resume: None,
            },
            crate::steps::Step {
                name: "b".into(),
                ixs: vec![],
                extra_signers: vec![],
                resume: None,
            },
        ];
        let signers = CeremonySigners {
            upgrade_authority: Some(Keypair::new()),
            payer: Arc::new(Keypair::new()),
            mint: Keypair::new(),
            treasury: Keypair::new(),
            vault: Keypair::new(),
        };
        let mut sender = FakeSender { calls: 0 };
        let halted =
            run_steps(&mut sender, &steps, &signers).expect_err("step b's confirm must fail");
        let expected_signature = halted
            .signature
            .expect("the halted step's signature is known before confirmation is attempted");

        let dir = tempfile::tempdir().expect("tempdir");
        let path = dir.path().join("signatures.json");
        let client = RpcClient::new_with_commitment(
            "http://127.0.0.1:1".to_string(),
            CommitmentConfig::finalized(),
        );
        let mut records = halted.records;
        persist_best_effort(&client, &mut records, false, &path);

        let written: serde_json::Value =
            serde_json::from_slice(&std::fs::read(&path).expect("file must exist")).unwrap();
        let entries = written.as_array().expect("array of records");
        assert!(
            entries.iter().any(|error| error["step"] == "b"
                && error["signature"] == expected_signature.to_string()),
            "the halted step's signature must be persisted even though confirmation failed: \
             {written}"
        );
        assert!(
            entries.iter().any(|error| error["step"] == "a"),
            "the step that landed before the halt must not be lost: {written}"
        );
    }

    #[test]
    fn resumed_config_check_refuses_mismatched_identities_naming_the_fields() {
        let keys = test_keys();
        let bytes = serialized_config(&keys);
        let mut other = test_keys();
        other.upgrade_authority = keys.upgrade_authority;
        other.treasury = keys.treasury;

        let err = check_resumed_config(Some(&bytes), &other)
            .expect_err("mismatched mint/vault must refuse")
            .to_string();
        assert!(err.contains("refusing to resume"), "got: {err}");
        assert!(
            err.contains("mint:"),
            "must name the mint divergence: {err}"
        );
        assert!(
            err.contains("vault:"),
            "must name the vault divergence: {err}"
        );
        assert!(
            !err.contains("treasury:"),
            "matching fields must not be listed: {err}"
        );

        let err = check_resumed_config(Some([1u8, 2, 3].as_slice()), &keys)
            .expect_err("undecodable config data must refuse, not pass")
            .to_string();
        assert!(err.contains("does not deserialize"), "got: {err}");
    }

    #[test]
    fn a_recorded_finalize_genesis_with_no_visible_config_refuses_to_resume() {
        let prior = vec![step_record(
            "distributor_finalize_genesis",
            Some(Signature::new_unique()),
            false,
        )];
        let err = check_finalize_genesis_not_in_flight(&prior, false)
            .expect_err("must refuse during the finalization window")
            .to_string();
        assert!(err.contains("finalize genesis"), "got: {err}");
        assert!(err.contains("in flight"), "got: {err}");
    }

    #[test]
    fn a_recorded_finalize_genesis_with_a_visible_config_resumes_normally() {
        let prior = vec![step_record(
            "distributor_finalize_genesis",
            Some(Signature::new_unique()),
            false,
        )];
        check_finalize_genesis_not_in_flight(&prior, true).expect("visible Config resumes");
    }

    #[test]
    fn require_resumed_config_matches_refuses_a_sent_finalize_genesis_with_absent_config() {
        let client = RpcClient::new_mock("succeeds");
        let keys = test_keys();
        let prior = vec![step_record(
            "distributor_finalize_genesis",
            Some(Signature::new_unique()),
            false,
        )];

        let err = require_resumed_config_matches(&client, &Pubkey::new_unique(), &keys, &prior)
            .expect_err(
                "a sent finalize genesis with an unobservable Config must refuse, wired end-to-end",
            );
        assert_eq!(exit_code_for(&err), exit_code::GATE_REFUSED);
        let msg = err.to_string();
        assert!(msg.contains("finalize genesis"), "got: {msg}");
        assert!(msg.contains("in flight"), "got: {msg}");
    }

    #[test]
    fn resume_reader_refuses_on_present_but_corrupt_signatures_json() {
        let dir = tempfile::tempdir().expect("tempdir");
        let path = dir.path().join("signatures.json");
        std::fs::write(&path, b"{ not valid json").expect("write corrupt file");

        let err = read_prior_records_strict(&path)
            .expect_err("a present-but-corrupt signatures.json must refuse, not look fresh");
        assert_eq!(exit_code_for(&err), exit_code::USAGE);
        let msg = err.to_string();
        assert!(msg.contains("not valid JSON"), "got: {msg}");
        assert!(msg.contains("finalize genesis"), "got: {msg}");
    }

    #[test]
    fn resume_reader_refuses_on_a_mangled_finalize_genesis_signature() {
        let dir = tempfile::tempdir().expect("tempdir");
        let path = dir.path().join("signatures.json");
        std::fs::write(
            &path,
            serde_json::to_vec(&serde_json::json!([{
                "step": "distributor_finalize_genesis",
                "signature": "not-a-real-base58-signature",
                "slot": serde_json::Value::Null,
                "skipped": false,
            }]))
            .expect("serialize"),
        )
        .expect("write file with a mangled signature");

        let err = read_prior_records_strict(&path)
            .expect_err("a mangled finalize genesis signature must refuse resume, not look fresh");
        assert_eq!(exit_code_for(&err), exit_code::USAGE);
        let msg = err.to_string();
        assert!(msg.contains("malformed"), "got: {msg}");
        assert!(msg.contains("finalize genesis"), "got: {msg}");
    }
}
