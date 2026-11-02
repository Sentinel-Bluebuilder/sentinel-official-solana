use crate::{PROGRAM_ID, program_data_address};
use anchor_lang::{InstructionData, ToAccountMetas};
use dvpn_core::args::{ConfigParamsUpdate, InitialConfigParams};
use solana_sdk::{instruction::Instruction, pubkey::Pubkey};

pub fn initialize_config_ix(
    authority: &Pubkey,
    rent_payer: &Pubkey,
    config: Pubkey,
    mint: Pubkey,
    vault: Pubkey,
    treasury: Pubkey,
    params: InitialConfigParams,
) -> Instruction {
    Instruction {
        program_id: PROGRAM_ID,
        accounts: dvpn::accounts::InitializeConfig {
            authority: *authority,
            program: PROGRAM_ID,
            program_data: program_data_address(),
            config,
            mint,
            vault,
            treasury,
            token_program: spl_token_2022_interface::id(),
            rent_payer: *rent_payer,
            system_program: anchor_lang::solana_program::system_program::ID,
        }
        .to_account_metas(None),
        data: dvpn::instruction::InitializeConfig {
            fee_bps: params.fee_bps,
            min_node_bond: params.min_node_bond,
            min_provider_bond: params.min_provider_bond,
            unbonding_seconds: params.unbonding_seconds,
            channel_close_grace_seconds: params.channel_close_grace_seconds,
            lease_close_grace_seconds: params.lease_close_grace_seconds,
            node_listing_ttl_seconds: params.node_listing_ttl_seconds,
            paused_mask: params.paused_mask,
        }
        .data(),
    }
}

pub fn set_params_ix(authority: &Pubkey, update: ConfigParamsUpdate) -> Instruction {
    let (config, _bump) = dvpn_core::seeds::find_config(&PROGRAM_ID);
    Instruction {
        program_id: PROGRAM_ID,
        accounts: dvpn::accounts::SetParams {
            authority: *authority,
            config,
        }
        .to_account_metas(None),
        data: dvpn::instruction::SetParams {
            fee_bps: update.fee_bps,
            min_node_bond: update.min_node_bond,
            min_provider_bond: update.min_provider_bond,
            unbonding_seconds: update.unbonding_seconds,
            channel_close_grace_seconds: update.channel_close_grace_seconds,
            lease_close_grace_seconds: update.lease_close_grace_seconds,
            node_listing_ttl_seconds: update.node_listing_ttl_seconds,
        }
        .data(),
    }
}

pub fn set_treasury_ix(authority: &Pubkey, treasury: Pubkey) -> Instruction {
    let (config, _bump) = dvpn_core::seeds::find_config(&PROGRAM_ID);
    Instruction {
        program_id: PROGRAM_ID,
        accounts: dvpn::accounts::SetTreasury {
            authority: *authority,
            config,
            treasury,
        }
        .to_account_metas(None),
        data: dvpn::instruction::SetTreasury {}.data(),
    }
}

pub fn set_paused_mask_ix(authority: &Pubkey, paused_mask: u8) -> Instruction {
    let (config, _bump) = dvpn_core::seeds::find_config(&PROGRAM_ID);
    Instruction {
        program_id: PROGRAM_ID,
        accounts: dvpn::accounts::SetPausedMask {
            authority: *authority,
            config,
        }
        .to_account_metas(None),
        data: dvpn::instruction::SetPausedMask { paused_mask }.data(),
    }
}

pub fn propose_authority_ix(authority: &Pubkey, new_authority: Pubkey) -> Instruction {
    let (config, _bump) = dvpn_core::seeds::find_config(&PROGRAM_ID);
    Instruction {
        program_id: PROGRAM_ID,
        accounts: dvpn::accounts::ProposeAuthority {
            authority: *authority,
            config,
        }
        .to_account_metas(None),
        data: dvpn::instruction::ProposeAuthority { new_authority }.data(),
    }
}

pub fn accept_authority_ix(authority: &Pubkey) -> Instruction {
    let (config, _bump) = dvpn_core::seeds::find_config(&PROGRAM_ID);
    Instruction {
        program_id: PROGRAM_ID,
        accounts: dvpn::accounts::AcceptAuthority {
            authority: *authority,
            config,
        }
        .to_account_metas(None),
        data: dvpn::instruction::AcceptAuthority {}.data(),
    }
}
