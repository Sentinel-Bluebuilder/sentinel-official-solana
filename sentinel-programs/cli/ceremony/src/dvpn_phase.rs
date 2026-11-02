use std::time::{Duration, Instant};

use anyhow::Context;
use solana_client::rpc_client::RpcClient;
use solana_commitment_config::CommitmentConfig;
use solana_pubkey::Pubkey;

use crate::dvpn_preconditions::{require_dvpn_config_matches, require_sha512_feature_active};
use crate::dvpn_steps::{
    STEP_DVPN_CREATE_FEE_TREASURY, STEP_DVPN_CREATE_VAULT, STEP_DVPN_INITIALIZE_CONFIG,
    STEP_DVPN_UNPAUSE, build_dvpn_create_fee_treasury_step, build_dvpn_create_vault_step,
    build_dvpn_initialize_config_step, build_dvpn_unpause_step, check_fee_treasury,
    default_initial_config_params, dvpn_vault_address, fee_treasury_address, has_dvpn_config,
    is_dvpn_config_unpaused,
};
use crate::genesis::require_program_id_not_on_other_cluster;
use crate::runner::{CeremonySigners, StepRecord, run_steps};
use crate::sender::{AccountReader, RpcTransport, Sender};

const FINALIZED_VISIBILITY_TIMEOUT: Duration = Duration::from_secs(30);
const FINALIZED_VISIBILITY_POLL_INTERVAL: Duration = Duration::from_millis(500);

fn wait_for_finalized_visibility(
    reader: &RpcTransport,
    account: &Pubkey,
) -> anyhow::Result<Vec<u8>> {
    let start = Instant::now();
    loop {
        if let Some(data) = reader.account_data(account) {
            return Ok(data);
        }
        if start.elapsed() >= FINALIZED_VISIBILITY_TIMEOUT {
            anyhow::bail!(
                "{account} not visible at finalized commitment after \
                 {FINALIZED_VISIBILITY_TIMEOUT:?}; refusing to proceed"
            );
        }
        std::thread::sleep(FINALIZED_VISIBILITY_POLL_INTERVAL);
    }
}

#[must_use]
pub fn dvpn_phase_step_names() -> Vec<&'static str> {
    vec![
        STEP_DVPN_CREATE_FEE_TREASURY,
        STEP_DVPN_CREATE_VAULT,
        STEP_DVPN_INITIALIZE_CONFIG,
        STEP_DVPN_UNPAUSE,
    ]
}

#[allow(clippy::too_many_arguments)]
pub fn run_dvpn_phase(
    sender: &mut dyn Sender,
    client: &RpcClient,
    other_cluster_client: Option<&RpcClient>,
    dvpn_program_id: &Pubkey,
    upgrade_authority: &Pubkey,
    payer: &Pubkey,
    mint: &Pubkey,
    fee_treasury_owner: &Pubkey,
    signers: &CeremonySigners,
    records: &mut Vec<StepRecord>,
) -> anyhow::Result<()> {
    if let Some(other_cluster_client) = other_cluster_client {
        require_program_id_not_on_other_cluster(dvpn_program_id, other_cluster_client)?;
    }

    let gate_reader = RpcTransport::new(client.url(), CommitmentConfig::finalized());
    let (config, _) = dvpn_core::seeds::find_config(dvpn_program_id);
    let dvpn_vault = dvpn_vault_address(&config, mint);
    let fee_treasury = fee_treasury_address(fee_treasury_owner, mint);

    let account_steps = vec![
        build_dvpn_create_fee_treasury_step(payer, fee_treasury_owner, mint, &config)?,
        build_dvpn_create_vault_step(payer, &config, mint),
    ];
    match run_steps(sender, &account_steps, signers) {
        Ok(landed) => records.extend(landed),
        Err(halted) => {
            records.extend(halted.records.clone());
            return Err(halted.into());
        }
    }

    let fee_treasury_data = wait_for_finalized_visibility(&gate_reader, &fee_treasury)
        .context("fee treasury not visible before the fee-treasury check")?;
    check_fee_treasury(&fee_treasury_data, fee_treasury_owner, mint)?;

    let config_already_initialized = has_dvpn_config(&gate_reader, &config);
    if config_already_initialized {
        eprintln!("skip (already done): {STEP_DVPN_INITIALIZE_CONFIG}");
        records.push(StepRecord {
            step: STEP_DVPN_INITIALIZE_CONFIG.into(),
            signature: None,
            slot: None,
            skipped: true,
        });
    } else {
        let initialize_config_step = build_dvpn_initialize_config_step(
            upgrade_authority,
            dvpn_program_id,
            &config,
            mint,
            &dvpn_vault,
            &fee_treasury,
            payer,
            &default_initial_config_params(),
        );
        match run_steps(
            sender,
            std::slice::from_ref(&initialize_config_step),
            signers,
        ) {
            Ok(landed) => records.extend(landed),
            Err(halted) => {
                records.extend(halted.records.clone());
                return Err(halted.into());
            }
        }
    }

    if is_dvpn_config_unpaused(&gate_reader, &config) {
        eprintln!("skip (already done): {STEP_DVPN_UNPAUSE}");
        records.push(StepRecord {
            step: STEP_DVPN_UNPAUSE.into(),
            signature: None,
            slot: None,
            skipped: true,
        });
        return Ok(());
    }

    wait_for_finalized_visibility(&gate_reader, &config)
        .context("dvpn Config not visible before the unpause gates")?;
    require_sha512_feature_active(&gate_reader)?;
    require_dvpn_config_matches(
        &gate_reader,
        &config,
        upgrade_authority,
        &dvpn_vault,
        mint,
        &fee_treasury,
    )?;

    let unpause_step = build_dvpn_unpause_step(dvpn_program_id, upgrade_authority);
    match run_steps(sender, std::slice::from_ref(&unpause_step), signers) {
        Ok(landed) => {
            records.extend(landed);
            Ok(())
        }
        Err(halted) => {
            records.extend(halted.records.clone());
            Err(halted.into())
        }
    }
}
