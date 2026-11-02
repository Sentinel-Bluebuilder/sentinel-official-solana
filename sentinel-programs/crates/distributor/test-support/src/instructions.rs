use anchor_lang::{InstructionData, ToAccountMetas};
use solana_sdk::{
    instruction::{AccountMeta, Instruction},
    pubkey::Pubkey,
    signature::Keypair,
    signer::Signer,
};

use crate::cosmos::ClaimSignature;
use crate::deploy::{PROGRAM_ID, find_program_data_address};
use crate::genesis::{MintAccounts, SingleLeafSetup};
use distributor_core::ceremony::CeremonyAddresses;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ClaimAccount {
    RentPayer = 0,
    Beneficiary = 1,
    Config = 2,
    Roots = 3,
    Bitmap = 4,
    Vault = 5,
    VaultAuthority = 6,
    Mint = 7,
    BeneficiaryToken = 8,
    TokenProgram = 9,
    AssociatedTokenProgram = 10,
    SystemProgram = 11,
    VestingState = 12,
    ScheduleBuffer = 13,
}

#[must_use]
pub fn with_account(
    mut instruction: Instruction,
    which: ClaimAccount,
    public_key: Pubkey,
) -> Instruction {
    instruction.accounts[which as usize].pubkey = public_key;
    instruction
}

pub const SCHEDULE_CHUNK_BYTES: usize = 900;

pub fn initialize_config_ix(
    upgrade_authority: &Keypair,
    mint_accounts: &MintAccounts,
    total_claimable: u64,
) -> Instruction {
    let (config, _) = distributor_core::pda::find_config(&PROGRAM_ID);
    Instruction {
        program_id: PROGRAM_ID,
        accounts: distributor::accounts::InitializeConfig {
            upgrade_authority: upgrade_authority.pubkey(),
            program: PROGRAM_ID,
            program_data: find_program_data_address(),
            config,
            mint: mint_accounts.mint,
            token_program: spl_token_2022_interface::id(),
            vault_authority: mint_accounts.vault_authority,
            vault: mint_accounts.vault,
            treasury: mint_accounts.treasury,
            system_program: anchor_lang::system_program::ID,
        }
        .to_account_metas(None),
        data: distributor::instruction::InitializeConfig { total_claimable }.data(),
    }
}

pub fn initialize_roots_ix(upgrade_authority: &Keypair) -> Instruction {
    let (config, _) = distributor_core::pda::find_config(&PROGRAM_ID);
    let (roots, _) = distributor_core::pda::find_roots(&PROGRAM_ID);
    Instruction {
        program_id: PROGRAM_ID,
        accounts: distributor::accounts::InitializeRoots {
            upgrade_authority: upgrade_authority.pubkey(),
            program: PROGRAM_ID,
            program_data: find_program_data_address(),
            config,
            roots,
            system_program: anchor_lang::system_program::ID,
        }
        .to_account_metas(None),
        data: distributor::instruction::InitializeRoots.data(),
    }
}

pub fn write_roots_chunk_ix(
    upgrade_authority: &Keypair,
    start_index: u16,
    chunk: &[[u8; 32]],
) -> Instruction {
    let (config, _) = distributor_core::pda::find_config(&PROGRAM_ID);
    let (roots, _) = distributor_core::pda::find_roots(&PROGRAM_ID);
    Instruction {
        program_id: PROGRAM_ID,
        accounts: distributor::accounts::WriteRootsChunk {
            upgrade_authority: upgrade_authority.pubkey(),
            program: PROGRAM_ID,
            program_data: find_program_data_address(),
            config,
            roots,
        }
        .to_account_metas(None),
        data: distributor::instruction::WriteRootsChunk {
            start_index,
            chunk: chunk.to_vec(),
        }
        .data(),
    }
}

pub fn initialize_bitmap_ix(
    upgrade_authority: &Keypair,
    shard_id: u16,
    leaf_count: u32,
) -> Instruction {
    let (config, _) = distributor_core::pda::find_config(&PROGRAM_ID);
    let (bitmap, _) = distributor_core::pda::find_claim_bitmap(&PROGRAM_ID, shard_id);
    Instruction {
        program_id: PROGRAM_ID,
        accounts: distributor::accounts::InitializeBitmap {
            upgrade_authority: upgrade_authority.pubkey(),
            program: PROGRAM_ID,
            program_data: find_program_data_address(),
            config,
            bitmap,
            system_program: anchor_lang::system_program::ID,
        }
        .to_account_metas(None),
        data: distributor::instruction::InitializeBitmap {
            shard_id,
            leaf_count,
        }
        .data(),
    }
}

pub fn finalize_genesis_ix(
    upgrade_authority: &Keypair,
    mint_accounts: &MintAccounts,
    expected_roots_commitment: [u8; 32],
) -> Instruction {
    let (config, _) = distributor_core::pda::find_config(&PROGRAM_ID);
    let (roots, _) = distributor_core::pda::find_roots(&PROGRAM_ID);
    Instruction {
        program_id: PROGRAM_ID,
        accounts: distributor::accounts::FinalizeGenesis {
            upgrade_authority: upgrade_authority.pubkey(),
            program: PROGRAM_ID,
            program_data: find_program_data_address(),
            config,
            roots,
            mint: mint_accounts.mint,
            vault: mint_accounts.vault,
            treasury: mint_accounts.treasury,
        }
        .to_account_metas(None),
        data: distributor::instruction::FinalizeGenesis {
            expected_roots_commitment,
        }
        .data(),
    }
}

pub fn set_total_claimable_ix(upgrade_authority: &Keypair, total_claimable: u64) -> Instruction {
    let (config, _) = distributor_core::pda::find_config(&PROGRAM_ID);
    Instruction {
        program_id: PROGRAM_ID,
        accounts: distributor::accounts::SetTotalClaimable {
            upgrade_authority: upgrade_authority.pubkey(),
            program: PROGRAM_ID,
            program_data: find_program_data_address(),
            config,
        }
        .to_account_metas(None),
        data: distributor::instruction::SetTotalClaimable { total_claimable }.data(),
    }
}

pub fn close_bitmap_ix(upgrade_authority: &Keypair, shard_id: u16) -> Instruction {
    let (config, _) = distributor_core::pda::find_config(&PROGRAM_ID);
    let (bitmap, _) = distributor_core::pda::find_claim_bitmap(&PROGRAM_ID, shard_id);
    Instruction {
        program_id: PROGRAM_ID,
        accounts: distributor::accounts::CloseBitmap {
            upgrade_authority: upgrade_authority.pubkey(),
            program: PROGRAM_ID,
            program_data: find_program_data_address(),
            config,
            bitmap,
        }
        .to_account_metas(None),
        data: distributor::instruction::CloseBitmap { shard_id }.data(),
    }
}

pub fn sweep_vault_excess_ix(
    upgrade_authority: &Keypair,
    mint_accounts: &MintAccounts,
) -> Instruction {
    let (config, _) = distributor_core::pda::find_config(&PROGRAM_ID);
    Instruction {
        program_id: PROGRAM_ID,
        accounts: distributor::accounts::SweepVaultExcess {
            upgrade_authority: upgrade_authority.pubkey(),
            program: PROGRAM_ID,
            program_data: find_program_data_address(),
            config,
            vault: mint_accounts.vault,
            treasury: mint_accounts.treasury,
            mint: mint_accounts.mint,
            vault_authority: mint_accounts.vault_authority,
            token_program: spl_token_2022_interface::id(),
        }
        .to_account_metas(None),
        data: distributor::instruction::SweepVaultExcess {}.data(),
    }
}

pub fn find_schedule_buffer_address(shard_id: u16, leaf_index: u32, rent_payer: &Pubkey) -> Pubkey {
    distributor_core::pda::find_schedule_buffer(&PROGRAM_ID, shard_id, leaf_index, rent_payer).0
}

pub fn stage_init_ix(
    rent_payer: &Keypair,
    shard_id: u16,
    leaf_index: u32,
    schedule_len: u32,
) -> Instruction {
    let schedule_buffer = find_schedule_buffer_address(shard_id, leaf_index, &rent_payer.pubkey());
    Instruction {
        program_id: PROGRAM_ID,
        accounts: distributor::accounts::StageInit {
            rent_payer: rent_payer.pubkey(),
            schedule_buffer,
            system_program: anchor_lang::system_program::ID,
        }
        .to_account_metas(None),
        data: distributor::instruction::StageInit {
            shard_id,
            leaf_index,
            schedule_len,
        }
        .data(),
    }
}

pub fn stage_write_ix(
    rent_payer: &Keypair,
    shard_id: u16,
    leaf_index: u32,
    offset: u32,
    bytes: Vec<u8>,
) -> Instruction {
    let schedule_buffer = find_schedule_buffer_address(shard_id, leaf_index, &rent_payer.pubkey());
    Instruction {
        program_id: PROGRAM_ID,
        accounts: distributor::accounts::StageWrite {
            rent_payer: rent_payer.pubkey(),
            schedule_buffer,
        }
        .to_account_metas(None),
        data: distributor::instruction::StageWrite {
            shard_id,
            leaf_index,
            offset,
            bytes,
        }
        .data(),
    }
}

pub fn stage_finalize_ix(
    rent_payer: &Keypair,
    shard_id: u16,
    leaf_index: u32,
    digest: [u8; 32],
) -> Instruction {
    let schedule_buffer = find_schedule_buffer_address(shard_id, leaf_index, &rent_payer.pubkey());
    Instruction {
        program_id: PROGRAM_ID,
        accounts: distributor::accounts::StageFinalize {
            rent_payer: rent_payer.pubkey(),
            schedule_buffer,
        }
        .to_account_metas(None),
        data: distributor::instruction::StageFinalize {
            shard_id,
            leaf_index,
            digest,
        }
        .data(),
    }
}

pub fn stage_abandon_ix(rent_payer: &Keypair, shard_id: u16, leaf_index: u32) -> Instruction {
    let schedule_buffer = find_schedule_buffer_address(shard_id, leaf_index, &rent_payer.pubkey());
    Instruction {
        program_id: PROGRAM_ID,
        accounts: distributor::accounts::StageAbandon {
            rent_payer: rent_payer.pubkey(),
            schedule_buffer,
        }
        .to_account_metas(None),
        data: distributor::instruction::StageAbandon {
            shard_id,
            leaf_index,
        }
        .data(),
    }
}

#[allow(clippy::too_many_arguments)]
pub fn claim_ix(
    rent_payer: &Keypair,
    beneficiary: &Pubkey,
    setup: &SingleLeafSetup,
    sig: &ClaimSignature,
    liquid_amount: u64,
    locked_amount: u64,
    schedule: Option<distributor_vesting::schedule::Schedule>,
) -> Instruction {
    let (config, _) = distributor_core::pda::find_config(&PROGRAM_ID);
    let (roots, _) = distributor_core::pda::find_roots(&PROGRAM_ID);
    let (bitmap, _) = distributor_core::pda::find_claim_bitmap(&PROGRAM_ID, setup.shard_id);
    let (vault_authority, _) = distributor_core::pda::find_vault_authority(&PROGRAM_ID);
    let (vesting_state, _) =
        distributor_core::pda::find_vesting_state(&PROGRAM_ID, setup.shard_id, setup.leaf_index);
    let token_program = spl_token_2022_interface::id();
    let beneficiary_token = setup.beneficiary_token(beneficiary);
    Instruction {
        program_id: PROGRAM_ID,
        accounts: distributor::accounts::Claim {
            rent_payer: rent_payer.pubkey(),
            beneficiary: *beneficiary,
            config,
            roots,
            bitmap,
            vault: setup.vault,
            vault_authority,
            mint: setup.mint,
            beneficiary_token,
            token_program,
            associated_token_program: spl_associated_token_account::id(),
            system_program: anchor_lang::system_program::ID,
            vesting_state,
            schedule_buffer: None,
        }
        .to_account_metas(None),
        data: distributor::instruction::Claim {
            args: distributor::instructions::ClaimArgs {
                shard_id: setup.shard_id,
                leaf_index: setup.leaf_index,
                bech32_address: setup.bech32_address.clone(),
                liquid_amount,
                locked_amount,
                vesting_schedule_hash: setup.vesting_schedule_hash,
                proof: setup.proof.clone(),
                signature: sig.signature,
                recovery_id: sig.recovery_id,
                schedule,
            },
        }
        .data(),
    }
}

pub fn withdraw_vested_ix(
    payer: &Keypair,
    beneficiary: &Pubkey,
    rent_recipient: &Pubkey,
    setup: &SingleLeafSetup,
) -> Instruction {
    let (config, _) = distributor_core::pda::find_config(&PROGRAM_ID);
    let (vault_authority, _) = distributor_core::pda::find_vault_authority(&PROGRAM_ID);
    let (vesting_state, _) =
        distributor_core::pda::find_vesting_state(&PROGRAM_ID, setup.shard_id, setup.leaf_index);
    let token_program = spl_token_2022_interface::id();
    let beneficiary_token = setup.beneficiary_token(beneficiary);
    Instruction {
        program_id: PROGRAM_ID,
        accounts: distributor::accounts::WithdrawVested {
            payer: payer.pubkey(),
            vesting_state,
            beneficiary: *beneficiary,
            rent_recipient: *rent_recipient,
            config,
            vault: setup.vault,
            vault_authority,
            mint: setup.mint,
            beneficiary_token,
            token_program,
            associated_token_program: spl_associated_token_account::id(),
            system_program: anchor_lang::system_program::ID,
        }
        .to_account_metas(None),
        data: distributor::instruction::WithdrawVested {
            shard_id: setup.shard_id,
            leaf_index: setup.leaf_index,
        }
        .data(),
    }
}

#[allow(clippy::too_many_arguments)]
pub fn claim_ix_with_buffer_rent_payer(
    rent_payer: &Keypair,
    beneficiary: &Pubkey,
    setup: &SingleLeafSetup,
    sig: &ClaimSignature,
    liquid_amount: u64,
    locked_amount: u64,
    buffer_rent_payer: &Pubkey,
) -> Instruction {
    let schedule_buffer =
        find_schedule_buffer_address(setup.shard_id, setup.leaf_index, buffer_rent_payer);
    claim_ix_with_buffer_account(
        rent_payer,
        beneficiary,
        setup,
        sig,
        liquid_amount,
        locked_amount,
        schedule_buffer,
    )
}

#[allow(clippy::too_many_arguments)]
pub fn claim_ix_with_buffer_account(
    rent_payer: &Keypair,
    beneficiary: &Pubkey,
    setup: &SingleLeafSetup,
    sig: &ClaimSignature,
    liquid_amount: u64,
    locked_amount: u64,
    schedule_buffer: Pubkey,
) -> Instruction {
    let (config, _) = distributor_core::pda::find_config(&PROGRAM_ID);
    let (roots, _) = distributor_core::pda::find_roots(&PROGRAM_ID);
    let (bitmap, _) = distributor_core::pda::find_claim_bitmap(&PROGRAM_ID, setup.shard_id);
    let (vault_authority, _) = distributor_core::pda::find_vault_authority(&PROGRAM_ID);
    let (vesting_state, _) =
        distributor_core::pda::find_vesting_state(&PROGRAM_ID, setup.shard_id, setup.leaf_index);
    let token_program = spl_token_2022_interface::id();
    let beneficiary_token = setup.beneficiary_token(beneficiary);
    Instruction {
        program_id: PROGRAM_ID,
        accounts: distributor::accounts::Claim {
            rent_payer: rent_payer.pubkey(),
            beneficiary: *beneficiary,
            config,
            roots,
            bitmap,
            vault: setup.vault,
            vault_authority,
            mint: setup.mint,
            beneficiary_token,
            token_program,
            associated_token_program: spl_associated_token_account::id(),
            system_program: anchor_lang::system_program::ID,
            vesting_state,
            schedule_buffer: Some(schedule_buffer),
        }
        .to_account_metas(None),
        data: distributor::instruction::Claim {
            args: distributor::instructions::ClaimArgs {
                shard_id: setup.shard_id,
                leaf_index: setup.leaf_index,
                bech32_address: setup.bech32_address.clone(),
                liquid_amount,
                locked_amount,
                vesting_schedule_hash: setup.vesting_schedule_hash,
                proof: setup.proof.clone(),
                signature: sig.signature,
                recovery_id: sig.recovery_id,
                schedule: None,
            },
        }
        .data(),
    }
}

#[must_use]
pub fn create_ata_ix(payer: &Pubkey, owner: &Pubkey, mint: &Pubkey) -> Instruction {
    spl_associated_token_account::instruction::create_associated_token_account(
        payer,
        owner,
        mint,
        &spl_token_2022_interface::id(),
    )
}

#[allow(clippy::too_many_arguments)]
pub fn claim_ix_with_program_id(
    program_id: &Pubkey,
    keys: &CeremonyAddresses,
    leaf: &distributor_merkle::BuiltLeaf,
    rent_payer: &Keypair,
    beneficiary: &Pubkey,
    sig: &ClaimSignature,
    liquid_amount: u64,
    locked_amount: u64,
    schedule: Option<distributor_vesting::schedule::Schedule>,
) -> Instruction {
    claim_ix_with_program_id_and_buffer(
        program_id,
        keys,
        leaf,
        rent_payer,
        beneficiary,
        sig,
        liquid_amount,
        locked_amount,
        schedule,
        None,
    )
}

#[allow(clippy::too_many_arguments)]
pub fn claim_ix_with_program_id_and_buffer(
    program_id: &Pubkey,
    keys: &CeremonyAddresses,
    leaf: &distributor_merkle::BuiltLeaf,
    rent_payer: &Keypair,
    beneficiary: &Pubkey,
    sig: &ClaimSignature,
    liquid_amount: u64,
    locked_amount: u64,
    schedule: Option<distributor_vesting::schedule::Schedule>,
    schedule_buffer: Option<Pubkey>,
) -> Instruction {
    let (config, _) = distributor_core::pda::find_config(program_id);
    let (roots, _) = distributor_core::pda::find_roots(program_id);
    let (bitmap, _) = distributor_core::pda::find_claim_bitmap(program_id, leaf.shard_id);
    let (vault_authority, _) = distributor_core::pda::find_vault_authority(program_id);
    let (vesting_state, _) =
        distributor_core::pda::find_vesting_state(program_id, leaf.shard_id, leaf.leaf_index);
    let token_program = spl_token_2022_interface::id();
    let mint = keys.mint;
    let beneficiary_token = ata(beneficiary, &mint);
    let mut accounts = distributor::accounts::Claim {
        rent_payer: rent_payer.pubkey(),
        beneficiary: *beneficiary,
        config,
        roots,
        bitmap,
        vault: keys.vault,
        vault_authority,
        mint,
        beneficiary_token,
        token_program,
        associated_token_program: spl_associated_token_account::id(),
        system_program: anchor_lang::system_program::ID,
        vesting_state,
        schedule_buffer,
    }
    .to_account_metas(None);
    if schedule_buffer.is_none() {
        let last = accounts.len() - 1;
        assert_eq!(
            accounts[last].pubkey,
            distributor::ID,
            "Claim.schedule_buffer must remain the last account (the Option None placeholder = distributor::ID); \
             if Claim's field order changed, this positional accounts[len()-1] patch targets the wrong meta"
        );
        accounts[last] = AccountMeta::new_readonly(*program_id, false);
    }
    Instruction {
        program_id: *program_id,
        accounts,
        data: distributor::instruction::Claim {
            args: distributor::instructions::ClaimArgs {
                shard_id: leaf.shard_id,
                leaf_index: leaf.leaf_index,
                bech32_address: leaf.bech32_address.clone(),
                liquid_amount,
                locked_amount,
                vesting_schedule_hash: leaf.vesting_schedule_hash,
                proof: leaf.proof.clone(),
                signature: sig.signature,
                recovery_id: sig.recovery_id,
                schedule,
            },
        }
        .data(),
    }
}

#[allow(clippy::too_many_arguments)]
pub fn claim_first_leaf_ix_with_program_id(
    program_id: &Pubkey,
    keys: &CeremonyAddresses,
    tree: &distributor_merkle::BuiltTree,
    rent_payer: &Keypair,
    beneficiary: &Pubkey,
    sig: &ClaimSignature,
    liquid_amount: u64,
    locked_amount: u64,
) -> Instruction {
    claim_ix_with_program_id(
        program_id,
        keys,
        &tree.leaves[0],
        rent_payer,
        beneficiary,
        sig,
        liquid_amount,
        locked_amount,
        None,
    )
}

pub fn ata(owner: &Pubkey, mint: &Pubkey) -> Pubkey {
    spl_associated_token_account::get_associated_token_address_with_program_id(
        owner,
        mint,
        &spl_token_2022_interface::id(),
    )
}

#[allow(clippy::too_many_arguments)]
pub fn withdraw_vested_ix_with_program_id(
    program_id: &Pubkey,
    keys: &CeremonyAddresses,
    shard_id: u16,
    leaf_index: u32,
    payer: &Pubkey,
    beneficiary: &Pubkey,
    rent_recipient: &Pubkey,
) -> Instruction {
    let (config, _) = distributor_core::pda::find_config(program_id);
    let (vault_authority, _) = distributor_core::pda::find_vault_authority(program_id);
    let (vesting_state, _) =
        distributor_core::pda::find_vesting_state(program_id, shard_id, leaf_index);
    let token_program = spl_token_2022_interface::id();
    let mint = keys.mint;
    let beneficiary_token = ata(beneficiary, &mint);
    Instruction {
        program_id: *program_id,
        accounts: distributor::accounts::WithdrawVested {
            payer: *payer,
            vesting_state,
            beneficiary: *beneficiary,
            rent_recipient: *rent_recipient,
            config,
            vault: keys.vault,
            vault_authority,
            mint,
            beneficiary_token,
            token_program,
            associated_token_program: spl_associated_token_account::id(),
            system_program: anchor_lang::system_program::ID,
        }
        .to_account_metas(None),
        data: distributor::instruction::WithdrawVested {
            shard_id,
            leaf_index,
        }
        .data(),
    }
}
