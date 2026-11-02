use super::run::run_genesis_phase;
use anyhow::Context;
use ceremony::artifacts::{Reconciliation, derive_supply};
use ceremony::inputs::{
    acquire_keys_lock, check_payer_distinct, load_or_create_keypair, log_resolved_keys, read_json,
    read_roots, require_tx_v1, shard_leaf_counts_from_manifest,
};
use ceremony::persist::{
    persist_best_effort, read_prior_records_strict, require_resumed_config_matches,
};
use ceremony::runner::CeremonySigners;
use ceremony::sender::{ROOTS_PER_CHUNK, RpcSender, RpcTransport};
use ceremony::steps::{CeremonyAddresses, CeremonyInputs, RentTable, Step, build_ceremony_steps};
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

fn is_mint_step(name: &str) -> bool {
    matches!(
        name,
        "distributor_create_mint"
            | "distributor_initialize_metadata"
            | "distributor_create_treasury"
            | "distributor_create_vault"
            | "distributor_mint_supply"
    )
}

fn load_inputs_and_rent(
    client: &RpcClient,
    dataset_dir: &Path,
    program_id: Pubkey,
) -> anyhow::Result<(CeremonyInputs, RentTable)> {
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
    Ok((
        CeremonyInputs {
            program_id,
            token_metadata,
            roots,
            shard_leaf_counts,
            vault_amount: derived.vault_amount,
            treasury_amount: derived.treasury_amount,
            expected_roots_commitment: derived.expected_roots_commitment,
        },
        RentTable {
            mint_at_create_lamports,
            mint_with_metadata_lamports,
            token_account_lamports,
        },
    ))
}

pub fn create_mint(
    dataset_dir: &Path,
    keys_dir: &Path,
    rpc_url: String,
    distributor_program_id: Pubkey,
    priority_fee_micro_lamports_per_cu: u64,
    json: bool,
) -> anyhow::Result<()> {
    let _keys_lock = acquire_keys_lock(keys_dir)?;
    let client = RpcClient::new_with_commitment(rpc_url, CommitmentConfig::finalized());
    let (inputs, rent) = load_inputs_and_rent(&client, dataset_dir, distributor_program_id)?;

    let resuming = keys_dir.join("signatures.json").exists();
    let payer_keypair = Arc::new(load_or_create_keypair(
        &keys_dir.join("payer.json"),
        resuming,
    )?);
    require_tx_v1(&client, &payer_keypair.pubkey())?;
    let mint_keypair = load_or_create_keypair(&keys_dir.join("mint.json"), resuming)?;
    let treasury_keypair = load_or_create_keypair(&keys_dir.join("treasury.json"), resuming)?;
    let vault_keypair = load_or_create_keypair(&keys_dir.join("vault.json"), resuming)?;
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
    let signers = CeremonySigners {
        upgrade_authority: Some(upgrade_authority_keypair),
        payer: payer_keypair,
        mint: mint_keypair,
        treasury: treasury_keypair,
        vault: vault_keypair,
    };

    let steps: Vec<Step> = build_ceremony_steps(&inputs, &keys, &rent, ROOTS_PER_CHUNK)
        .into_iter()
        .filter(|step| is_mint_step(&step.name))
        .collect();
    let step_client = RpcTransport::new(client.url(), CommitmentConfig::confirmed());
    let mut sender = RpcSender::new(
        step_client,
        &signers.payer,
        signers.upgrade_authority.as_ref(),
        priority_fee_micro_lamports_per_cu,
    );
    let sig_path = keys_dir.join("signatures.json");
    let mut records = run_genesis_phase(
        &mut sender,
        &steps,
        &signers,
        &client,
        json,
        &sig_path,
        false,
    )?;
    persist_best_effort(&client, &mut records, json, &sig_path);
    eprintln!("mint created: {}", keys.mint);
    Ok(())
}

pub fn init_distributor(
    dataset_dir: &Path,
    keys_dir: &Path,
    rpc_url: String,
    distributor_program_id: Pubkey,
    priority_fee_micro_lamports_per_cu: u64,
    json: bool,
) -> anyhow::Result<()> {
    let _keys_lock = acquire_keys_lock(keys_dir)?;
    tracing::info!(%distributor_program_id, "initializing program state");
    let client = RpcClient::new_with_commitment(rpc_url, CommitmentConfig::finalized());
    let (inputs, rent) = load_inputs_and_rent(&client, dataset_dir, distributor_program_id)?;

    let resuming = keys_dir.join("signatures.json").exists();
    let payer_keypair = Arc::new(load_or_create_keypair(
        &keys_dir.join("payer.json"),
        resuming,
    )?);
    require_tx_v1(&client, &payer_keypair.pubkey())?;

    let mint_keypair = load_or_create_keypair(&keys_dir.join("mint.json"), true)?;
    let treasury_keypair = load_or_create_keypair(&keys_dir.join("treasury.json"), true)?;
    let vault_keypair = load_or_create_keypair(&keys_dir.join("vault.json"), true)?;
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
        &distributor_program_id,
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

    let steps: Vec<Step> = build_ceremony_steps(&inputs, &keys, &rent, ROOTS_PER_CHUNK)
        .into_iter()
        .filter(|step| !is_mint_step(&step.name))
        .collect();
    let step_client = RpcTransport::new(client.url(), CommitmentConfig::confirmed());
    let mut sender = RpcSender::new(
        step_client,
        &signers.payer,
        signers.upgrade_authority.as_ref(),
        priority_fee_micro_lamports_per_cu,
    );
    let sig_path = keys_dir.join("signatures.json");
    let mut records = run_genesis_phase(
        &mut sender,
        &steps,
        &signers,
        &client,
        json,
        &sig_path,
        false,
    )?;
    persist_best_effort(&client, &mut records, json, &sig_path);
    eprintln!("distributor: program state initialized");
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
    fn test_keys() -> CeremonyAddresses {
        CeremonyAddresses {
            upgrade_authority: Pubkey::new_unique(),
            payer: Pubkey::new_unique(),
            mint: Pubkey::new_unique(),
            treasury: Pubkey::new_unique(),
            vault: Pubkey::new_unique(),
        }
    }

    fn test_ceremony_inputs() -> CeremonyInputs {
        CeremonyInputs {
            program_id: Pubkey::new_unique(),
            token_metadata: TokenMetadataParams {
                name: "Sentinel".into(),
                symbol: "DVPN".into(),
                uri: "https://sentinel.co/metadata.json".into(),
            },
            roots: vec![[0u8; 32]],
            shard_leaf_counts: vec![0u32],
            vault_amount: 0,
            treasury_amount: 0,
            expected_roots_commitment: [0u8; 32],
        }
    }

    fn test_rent_table() -> RentTable {
        RentTable {
            mint_at_create_lamports: 0,
            mint_with_metadata_lamports: 0,
            token_account_lamports: 0,
        }
    }

    #[test]
    fn is_mint_step_partitions_build_ceremony_steps_exactly() {
        let steps = build_ceremony_steps(
            &test_ceremony_inputs(),
            &test_keys(),
            &test_rent_table(),
            ROOTS_PER_CHUNK,
        );

        let mut mint_steps: Vec<&str> = steps
            .iter()
            .map(|step| step.name.as_str())
            .filter(|name| is_mint_step(name))
            .collect();
        mint_steps.sort_unstable();
        let mut expected = vec![
            "distributor_create_mint",
            "distributor_create_treasury",
            "distributor_create_vault",
            "distributor_initialize_metadata",
            "distributor_mint_supply",
        ];
        expected.sort_unstable();
        assert_eq!(
            mint_steps, expected,
            "is_mint_step must select exactly these 5"
        );

        let init_steps: Vec<&str> = steps
            .iter()
            .map(|step| step.name.as_str())
            .filter(|name| !is_mint_step(name))
            .collect();
        assert!(
            !init_steps.is_empty(),
            "sanity: build_ceremony_steps also produces init-side steps"
        );
        assert_eq!(
            mint_steps.len() + init_steps.len(),
            steps.len(),
            "every step must land on exactly one side"
        );
        let mut union: Vec<&str> = mint_steps
            .iter()
            .chain(init_steps.iter())
            .copied()
            .collect();
        union.sort_unstable();
        let mut all_names: Vec<&str> = steps.iter().map(|step| step.name.as_str()).collect();
        all_names.sort_unstable();
        assert_eq!(
            union, all_names,
            "create-mint's steps union init's steps must equal the full step set"
        );
    }
}
