use crate::sender::AccountReader;
use crate::steps::{ResumeHint, Step};
use anchor_lang::AccountDeserialize;
use spl_token_2022_interface::extension::StateWithExtensions;
use spl_token_2022_interface::state::Mint;

pub fn is_config_step_done(step_name: &str, config_data: Option<&[u8]>) -> bool {
    let Some(mut data) = config_data else {
        return false;
    };
    if step_name == "distributor_initialize_config" {
        return true;
    }
    distributor::state::Config::try_deserialize(&mut data)
        .map(|config| config.finalized)
        .unwrap_or(false)
}

#[must_use]
pub fn is_metadata_step_done(mint_data: Option<&[u8]>) -> bool {
    match mint_data {
        Some(data) => data.len() > distributor_mint::create::mint_account_space(),
        None => false,
    }
}

#[must_use]
pub fn is_mint_supply_done(mint_data: Option<&[u8]>) -> bool {
    let Some(data) = mint_data else {
        return false;
    };
    StateWithExtensions::<Mint>::unpack(data)
        .map(|mint_state| mint_state.base.mint_authority.is_none())
        .unwrap_or(false)
}

#[must_use]
pub fn is_roots_chunk_done(
    roots_data: Option<&[u8]>,
    start_index: usize,
    chunk: &[[u8; 32]],
) -> bool {
    let Some(data) = roots_data else {
        return false;
    };
    if chunk.iter().all(|root| *root == [0u8; 32]) {
        return false;
    }

    let start = 8usize.saturating_add(start_index.saturating_mul(32));
    let end = start.saturating_add(chunk.len().saturating_mul(32));
    let Some(window) = data.get(start..end) else {
        return false;
    };
    let expected: Vec<u8> = chunk.iter().flatten().copied().collect();
    window == expected.as_slice()
}

pub fn is_step_done<R: AccountReader + ?Sized>(reader: &R, step: &Step) -> bool {
    if let [new_account] = step.extra_signers.as_slice()
        && (step.name == "distributor_create_mint"
            || step.name == "distributor_create_treasury"
            || step.name == "distributor_create_vault")
    {
        return reader.account_exists(new_account);
    }
    if step.name == "distributor_initialize_metadata" {
        let mint = step
            .ixs
            .last()
            .and_then(|instruction| instruction.accounts.first())
            .map(|account_meta| account_meta.pubkey);
        let data = mint.and_then(|public_key| reader.account_data(&public_key));
        return is_metadata_step_done(data.as_deref());
    }
    if step.name == "distributor_mint_supply" {
        let mint = step
            .ixs
            .first()
            .and_then(|instruction| instruction.accounts.first())
            .map(|account_meta| account_meta.pubkey);
        let data = mint.and_then(|public_key| reader.account_data(&public_key));
        return is_mint_supply_done(data.as_deref());
    }
    if step.name == "distributor_initialize_config" || step.name == "distributor_finalize_genesis" {
        let Some(program_id) = step.ixs.first().map(|instruction| instruction.program_id) else {
            return false;
        };
        let (config_pda, _) = distributor_core::pda::find_config(&program_id);
        let data = reader.account_data(&config_pda);
        return is_config_step_done(&step.name, data.as_deref());
    }
    if step.name == "distributor_initialize_roots" {
        let Some(program_id) = step.ixs.first().map(|instruction| instruction.program_id) else {
            return false;
        };
        let (roots_pda, _) = distributor_core::pda::find_roots(&program_id);
        return reader.account_exists(&roots_pda);
    }
    if let Some(shard_id) = step.name.strip_prefix("distributor_initialize_bitmap_") {
        let Some(program_id) = step.ixs.first().map(|instruction| instruction.program_id) else {
            return false;
        };
        let Ok(shard_id) = shard_id.parse::<u16>() else {
            return false;
        };
        let (bitmap_pda, _) = distributor_core::pda::find_claim_bitmap(&program_id, shard_id);
        return reader.account_exists(&bitmap_pda);
    }

    match &step.resume {
        Some(ResumeHint::RootsChunk { start_index, chunk }) => {
            let Some(program_id) = step.ixs.first().map(|instruction| instruction.program_id)
            else {
                return false;
            };
            let (roots_pda, _) = distributor_core::pda::find_roots(&program_id);
            let data = reader.account_data(&roots_pda);
            is_roots_chunk_done(data.as_deref(), *start_index, chunk)
        }
        None => false,
    }
}
