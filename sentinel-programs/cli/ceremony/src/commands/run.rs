use crate::Cluster;
use anyhow::Context;
use ceremony::artifacts::{Reconciliation, derive_supply};
use ceremony::data_availability::{UreqTransport, require_retrievable};
use ceremony::dry_run::DryRunTransport;
use ceremony::genesis::require_program_id_not_on_other_cluster;
use ceremony::inputs::{
    acquire_keys_lock, check_payer_distinct, load_or_create_keypair, log_resolved_keys, read_json,
    read_roots, require_tx_v1, resolve_program_id, shard_leaf_counts_from_manifest,
};
use ceremony::persist::{
    persist_best_effort, read_prior_records_strict, require_resumed_config_matches,
    step_record_json,
};
use ceremony::runner::{CeremonySigners, StepRecord, run_steps};
use ceremony::sender::{ROOTS_PER_CHUNK, RpcSender, RpcTransport, Sender};
use ceremony::steps::{
    CeremonyAddresses, CeremonyInputs, RentTable, Step, build_ceremony_steps,
    build_finalize_genesis_step,
};
use ceremony::usage::{TagUsage, usage_error};
use distributor_dataset::Attestation;
use distributor_mint::create::TokenMetadataParams;
use solana_client::rpc_client::RpcClient;
use solana_commitment_config::CommitmentConfig;
use solana_keypair::read_keypair_file;
use solana_pubkey::Pubkey;
use solana_signer::Signer;
use std::path::Path;
use std::sync::Arc;
use std::time::Instant;

const PHASE_COUNT: u32 = 7;

fn phase_start(phase: u32, label: &str) -> Instant {
    tracing::info!(phase, total = PHASE_COUNT, label, "phase started");
    Instant::now()
}

fn phase_done(started: Instant, phase: u32, label: &str) {
    tracing::info!(
        phase,
        total = PHASE_COUNT,
        label,
        elapsed = ?started.elapsed(),
        "phase complete"
    );
}

#[allow(clippy::too_many_arguments)]
fn run_finalize_genesis_phase(
    sender: &mut dyn Sender,
    slot_client: &RpcClient,
    inputs: &CeremonyInputs,
    keys: &CeremonyAddresses,
    signers: &CeremonySigners,
    records: &mut Vec<StepRecord>,
    keys_dir: &Path,
    da_url: &str,
    json: bool,
    dry_run: bool,
    ceremony_started: Instant,
) -> anyhow::Result<()> {
    let sig_path = keys_dir.join("signatures.json");

    let phase_started = phase_start(6, "finalize genesis");
    let da_transport = UreqTransport::default();
    require_retrievable(&da_transport, da_url, &inputs.expected_roots_commitment).inspect_err(
        |_| {
            if !dry_run {
                persist_best_effort(slot_client, records, json, &sig_path);
            }
        },
    )?;
    eprintln!("claim dataset verified retrievable at {da_url}; opening claims");

    let finalize_genesis_step = build_finalize_genesis_step(inputs, keys);
    let finalize_genesis_records = match run_steps(
        sender,
        std::slice::from_ref(&finalize_genesis_step),
        signers,
    ) {
        Ok(step_records) => step_records,
        Err(error) => {
            if !dry_run {
                records.extend(error.records.clone());
                persist_best_effort(slot_client, records, json, &sig_path);
            }
            return Err(error.into());
        }
    };

    let Some(_finalize_genesis_record) = finalize_genesis_records.first().cloned() else {
        anyhow::bail!("finalize genesis produced no step record");
    };
    records.extend(finalize_genesis_records);

    phase_done(phase_started, 6, "finalize genesis");
    let summary = |records: &[StepRecord]| {
        tracing::info!(
            steps = records.len(),
            elapsed = ?ceremony_started.elapsed(),
            "ceremony run complete"
        );
    };

    if dry_run {
        if json {
            for record in records.iter() {
                println!("{}", step_record_json(record));
            }
        }
        summary(records);
        return Ok(());
    }

    persist_best_effort(slot_client, records, json, &sig_path);
    summary(records);

    Ok(())
}

pub fn run_genesis_phase(
    sender: &mut dyn Sender,
    genesis_steps: &[Step],
    signers: &CeremonySigners,
    slot_client: &RpcClient,
    json: bool,
    sig_path: &Path,
    dry_run: bool,
) -> anyhow::Result<Vec<StepRecord>> {
    match run_steps(sender, genesis_steps, signers) {
        Ok(step_records) => Ok(step_records),
        Err(error) => {
            if !dry_run {
                let mut halted = error.records.clone();

                persist_best_effort(slot_client, &mut halted, json, sig_path);
            }
            Err(error.into())
        }
    }
}

#[allow(clippy::too_many_arguments)]
pub fn run(
    dataset_dir: &Path,
    keys_dir: &Path,
    rpc_url: String,
    cluster: Cluster,
    other_rpc_url: Option<String>,
    allow_unknown_genesis: bool,
    priority_fee_micro_lamports_per_cu: u64,
    distributor_program_id: Option<Pubkey>,
    da_url: String,
    dvpn_program_id: Option<Pubkey>,
    fee_treasury_owner: Option<Pubkey>,
    skip_dvpn: bool,
    json: bool,
    dry_run: bool,
) -> anyhow::Result<()> {
    if other_rpc_url.is_none() && !allow_unknown_genesis {
        return Err(usage_error(
            "--other-rpc-url is required unless --allow-unknown-genesis is set (single-node/\
             localnet mode has no paired cluster's RPC endpoint to check)",
        ));
    }

    let dvpn_inputs: Option<(Pubkey, Pubkey)> = if skip_dvpn {
        None
    } else {
        match (dvpn_program_id, fee_treasury_owner) {
            (Some(program_id), Some(owner)) => Some((program_id, owner)),
            _ => {
                return Err(usage_error(
                    "--dvpn-program-id and --fee-treasury-owner are required unless \
                     --skip-dvpn is set",
                ));
            }
        }
    };

    let _keys_lock = acquire_keys_lock(keys_dir)?;
    let ceremony_started = Instant::now();

    let phase_started = phase_start(0, "genesis-hash cluster binding");
    let client = RpcClient::new_with_commitment(rpc_url.clone(), CommitmentConfig::finalized());
    let (expected_name, expected_hash) = cluster.genesis();
    ceremony::genesis::require_genesis_matches(
        &client,
        "--rpc-url",
        expected_name,
        expected_hash,
        allow_unknown_genesis,
    )?;
    let other_cluster_client = match &other_rpc_url {
        Some(other_rpc_url) => {
            let other_cluster_client = RpcClient::new_with_commitment(
                other_rpc_url.clone(),
                CommitmentConfig::finalized(),
            );
            let (other_name, other_hash) = cluster.opposite().genesis();
            ceremony::genesis::require_genesis_matches(
                &other_cluster_client,
                "--other-rpc-url",
                other_name,
                other_hash,
                allow_unknown_genesis,
            )?;
            tracing::info!(
                rpc_url_cluster = expected_name,
                other_rpc_url_cluster = other_name,
                "genesis hash verified"
            );
            Some(other_cluster_client)
        }
        None => {
            tracing::info!(
                rpc_url_cluster = expected_name,
                "genesis hash verified; --other-rpc-url omitted under --allow-unknown-genesis, \
                 cross-cluster program-id gate skipped"
            );
            None
        }
    };

    phase_done(phase_started, 0, "genesis-hash cluster binding");

    let program_id = resolve_program_id(
        distributor_program_id,
        &keys_dir.join("distributor-program.json"),
    )?;

    let phase_started = phase_start(1, "cross-cluster program-id check");
    if let Some(other_cluster_client) = &other_cluster_client {
        require_program_id_not_on_other_cluster(&program_id, other_cluster_client)?;
    }
    phase_done(phase_started, 1, "cross-cluster program-id check");

    let phase_started = phase_start(2, "dataset load and verify");
    let attestation: Attestation = read_json(&dataset_dir.join("attestation.json")).tag_usage()?;
    let reconciliation: Reconciliation =
        read_json(&dataset_dir.join("reconciliation.json")).tag_usage()?;
    let derived = derive_supply(&attestation, &reconciliation)?;
    let roots = read_roots(dataset_dir).tag_usage()?;

    distributor_dataset::require_valid_dataset(dataset_dir)
        .context("dataset self-verify failed")
        .tag_usage()?;
    let shard_leaf_counts = shard_leaf_counts_from_manifest(dataset_dir).tag_usage()?;
    if distributor_merkle::roots_commitment(&roots, &shard_leaf_counts, attestation.total_claimable)
        != derived.expected_roots_commitment
    {
        return Err(usage_error(
            "roots.json + manifest leaf counts do not reproduce the attestation's \
             roots_commitment",
        ));
    }
    phase_done(phase_started, 2, "dataset load and verify");

    let phase_started = phase_start(3, "rent fetch");
    let slot_client = RpcClient::new_with_commitment(rpc_url, CommitmentConfig::finalized());
    let token_metadata = TokenMetadataParams {
        name: "Sentinel".into(),
        symbol: "DVPN".into(),
        uri: "https://sentinel.co/metadata.json".into(),
    };
    let mint_at_create_lamports = client
        .get_minimum_balance_for_rent_exemption(distributor_mint::create::mint_account_space())
        .context("failed to fetch rent")?;
    let mint_with_metadata_lamports = client
        .get_minimum_balance_for_rent_exemption(
            distributor_mint::create::mint_account_space().saturating_add(
                distributor_mint::supply::metadata_tlv_space(&token_metadata),
            ),
        )
        .context("failed to fetch rent")?;
    let token_account_lamports = client
        .get_minimum_balance_for_rent_exemption(distributor_mint::supply::token_account_space())
        .context("failed to fetch rent")?;
    let rent = RentTable {
        mint_at_create_lamports,
        mint_with_metadata_lamports,
        token_account_lamports,
    };
    phase_done(phase_started, 3, "rent fetch");

    let phase_started = phase_start(4, "keys resolution");
    let resuming = keys_dir.join("signatures.json").exists();
    let payer_keypair = Arc::new(load_or_create_keypair(
        &keys_dir.join("payer.json"),
        resuming,
    )?);
    let mint_keypair = load_or_create_keypair(&keys_dir.join("mint.json"), resuming)?;
    let treasury_keypair = load_or_create_keypair(&keys_dir.join("treasury.json"), resuming)?;
    let vault_keypair = load_or_create_keypair(&keys_dir.join("vault.json"), resuming)?;

    require_tx_v1(&client, &payer_keypair.pubkey())?;

    let inputs = CeremonyInputs {
        program_id,
        token_metadata,
        roots,
        shard_leaf_counts,
        vault_amount: derived.vault_amount,
        treasury_amount: derived.treasury_amount,
        expected_roots_commitment: derived.expected_roots_commitment,
    };

    let upgrade_authority_keypair = read_keypair_file(keys_dir.join("upgrade_authority.json"))
        .map_err(|error| anyhow::anyhow!("failed to read upgrade_authority keypair: {error}"))
        .tag_usage()?;
    check_payer_distinct(&payer_keypair.pubkey(), &upgrade_authority_keypair.pubkey())?;
    let keys = CeremonyAddresses {
        upgrade_authority: upgrade_authority_keypair.pubkey(),
        payer: payer_keypair.pubkey(),
        mint: mint_keypair.pubkey(),
        treasury: treasury_keypair.pubkey(),
        vault: vault_keypair.pubkey(),
    };
    log_resolved_keys(&keys);
    require_resumed_config_matches(
        &client,
        &program_id,
        &keys,
        &read_prior_records_strict(&keys_dir.join("signatures.json"))?,
    )?;
    let signers = CeremonySigners {
        upgrade_authority: Some(upgrade_authority_keypair),
        payer: payer_keypair,
        mint: mint_keypair,
        treasury: treasury_keypair,
        vault: vault_keypair,
    };
    phase_done(phase_started, 4, "keys resolution");

    let sig_path = keys_dir.join("signatures.json");

    let genesis_steps = build_ceremony_steps(&inputs, &keys, &rent, ROOTS_PER_CHUNK);
    let phase_started = phase_start(5, "genesis steps");
    tracing::info!(steps = genesis_steps.len(), "genesis phase steps built");

    let step_client = RpcTransport::new(client.url(), CommitmentConfig::confirmed());

    let mut sender: Box<dyn Sender + '_> = if dry_run {
        Box::new(RpcSender::new(
            DryRunTransport(step_client),
            &signers.payer,
            signers.upgrade_authority.as_ref(),
            priority_fee_micro_lamports_per_cu,
        ))
    } else {
        Box::new(RpcSender::new(
            step_client,
            &signers.payer,
            signers.upgrade_authority.as_ref(),
            priority_fee_micro_lamports_per_cu,
        ))
    };

    let mut records = run_genesis_phase(
        sender.as_mut(),
        &genesis_steps,
        &signers,
        &slot_client,
        json,
        &sig_path,
        dry_run,
    )?;
    phase_done(phase_started, 5, "genesis steps");

    run_finalize_genesis_phase(
        sender.as_mut(),
        &slot_client,
        &inputs,
        &keys,
        &signers,
        &mut records,
        keys_dir,
        &da_url,
        json,
        dry_run,
        ceremony_started,
    )?;

    if let Some((dvpn_program_id, fee_treasury_owner)) = dvpn_inputs {
        let mut dvpn_records: Vec<StepRecord> = Vec::new();
        let result = ceremony::dvpn_phase::run_dvpn_phase(
            sender.as_mut(),
            &client,
            other_cluster_client.as_ref(),
            &dvpn_program_id,
            &keys.upgrade_authority,
            &keys.payer,
            &keys.mint,
            &fee_treasury_owner,
            &signers,
            &mut dvpn_records,
        );
        if !dry_run {
            persist_best_effort(&client, &mut dvpn_records, json, &sig_path);
        }
        result?;
        eprintln!("dvpn phase complete: program {dvpn_program_id}");
    }
    if dry_run {
        eprintln!("dry-run complete: program {program_id} (nothing was submitted)");
    } else {
        eprintln!("ceremony complete: program {program_id}");
    }
    Ok(())
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
    use anyhow::Context as _;
    use ceremony::CeremonyError;
    use ceremony::exit_code;
    use ceremony::runner::{CeremonySigners, HaltedStep, StepRecord, run_steps};
    use ceremony::sender::{AccountReader, RpcSender, Sender, Transport};
    use ceremony::steps::{
        CeremonyAddresses, CeremonyInputs, RentTable, Step, build_ceremony_steps,
    };
    use ceremony::usage::exit_code_for;
    use solana_instruction::Instruction;
    use solana_keypair::Keypair;
    use solana_pubkey::Pubkey;
    use solana_signature::Signature;
    use solana_signer::Signer;
    use solana_transaction::versioned::VersionedTransaction;
    use txv1::{TxConfig, build_signed};
    #[test]
    fn run_genesis_phase_returns_every_step_record_for_both_dry_run_and_real() {
        struct FakeTransport;
        impl AccountReader for FakeTransport {
            fn account_exists(&self, _address: &Pubkey) -> bool {
                false
            }
            fn account_data(&self, _address: &Pubkey) -> Option<Vec<u8>> {
                None
            }
        }
        impl Transport for FakeTransport {
            fn latest_blockhash(&self) -> Result<solana_hash::Hash, CeremonyError> {
                Ok(solana_hash::Hash::default())
            }
            fn submit(&mut self, _tx: &VersionedTransaction) -> Result<Signature, CeremonyError> {
                Ok(Signature::default())
            }
        }

        let payer = Keypair::new();
        let upgrade_authority = Keypair::new();
        let signers = CeremonySigners {
            upgrade_authority: Some(Keypair::new()),
            payer: Arc::new(Keypair::new()),
            mint: Keypair::new(),
            treasury: Keypair::new(),
            vault: Keypair::new(),
        };
        let genesis_steps = vec![
            ceremony::steps::Step {
                name: "distributor_create_mint".into(),
                ixs: vec![],
                extra_signers: vec![],
                resume: None,
            },
            ceremony::steps::Step {
                name: "distributor_create_treasury".into(),
                ixs: vec![],
                extra_signers: vec![],
                resume: None,
            },
        ];

        let slot_client = RpcClient::new_with_commitment(
            "http://127.0.0.1:1".to_string(),
            CommitmentConfig::finalized(),
        );
        let dir = tempfile::tempdir().expect("tempdir");
        let sig_path = dir.path().join("signatures.json");

        for dry_run in [true, false] {
            let mut sender: Box<dyn Sender> = if dry_run {
                Box::new(RpcSender::new(
                    DryRunTransport(FakeTransport),
                    &payer,
                    Some(&upgrade_authority),
                    0,
                ))
            } else {
                Box::new(RpcSender::new(
                    FakeTransport,
                    &payer,
                    Some(&upgrade_authority),
                    0,
                ))
            };
            let records = run_genesis_phase(
                sender.as_mut(),
                &genesis_steps,
                &signers,
                &slot_client,
                false,
                &sig_path,
                dry_run,
            )
            .expect("a never-failing transport must not error");
            assert_eq!(
                records.len(),
                2,
                "dry_run={dry_run}: both genesis steps must be recorded, not an empty Vec"
            );
            assert_eq!(records[0].step, "distributor_create_mint");
            assert_eq!(records[1].step, "distributor_create_treasury");
        }
    }
}
