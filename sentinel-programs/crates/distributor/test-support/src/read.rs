use anchor_lang::AccountDeserialize;
use litesvm::LiteSVM;
use solana_sdk::{pubkey::Pubkey, signature::Keypair, signer::Signer};

use crate::deploy::PROGRAM_ID;

pub fn find_vesting_state_address_with_program_id(
    program_id: &Pubkey,
    shard_id: u16,
    leaf_index: u32,
) -> Pubkey {
    distributor_core::pda::find_vesting_state(program_id, shard_id, leaf_index).0
}

pub fn find_vesting_state_address(shard_id: u16, leaf_index: u32) -> Pubkey {
    find_vesting_state_address_with_program_id(&PROGRAM_ID, shard_id, leaf_index)
}

#[track_caller]
pub fn read_vesting_state(svm: &LiteSVM, pda: Pubkey) -> distributor::state::VestingState {
    let account = svm.get_account(&pda).expect("vesting_state account exists");
    distributor::state::VestingState::try_deserialize(&mut account.data.as_slice())
        .expect("vesting_state account should deserialize")
}

#[track_caller]
pub fn read_bitmap_with_program_id(
    svm: &LiteSVM,
    program_id: &Pubkey,
    shard_id: u16,
) -> distributor::state::ClaimBitmap {
    let (bitmap, _) = distributor_core::pda::find_claim_bitmap(program_id, shard_id);
    let account = svm.get_account(&bitmap).expect("bitmap account exists");
    distributor::state::ClaimBitmap::try_deserialize(&mut account.data.as_slice())
        .expect("bitmap account should deserialize")
}

#[track_caller]
pub fn read_bitmap(svm: &LiteSVM, shard_id: u16) -> distributor::state::ClaimBitmap {
    read_bitmap_with_program_id(svm, &PROGRAM_ID, shard_id)
}

#[track_caller]
pub fn read_config_with_program_id(
    svm: &LiteSVM,
    program_id: &Pubkey,
) -> distributor::state::Config {
    let (config, _) = distributor_core::pda::find_config(program_id);
    let account = svm.get_account(&config).expect("config account exists");
    distributor::state::Config::try_deserialize(&mut account.data.as_slice())
        .expect("config account should deserialize")
}

#[track_caller]
pub fn read_config(svm: &LiteSVM) -> distributor::state::Config {
    read_config_with_program_id(svm, &PROGRAM_ID)
}

#[track_caller]
pub fn read_roots_with_program_id(
    svm: &LiteSVM,
    program_id: &Pubkey,
) -> [[u8; 32]; distributor_core::consts::SHARD_COUNT as usize] {
    let (roots, _) = distributor_core::pda::find_roots(program_id);
    let account = svm.get_account(&roots).expect("roots account exists");
    let roots = distributor::state::Roots::try_deserialize(&mut account.data.as_slice())
        .expect("roots account should deserialize");
    roots.shard_roots
}

#[track_caller]
pub fn read_roots(svm: &LiteSVM) -> [[u8; 32]; distributor_core::consts::SHARD_COUNT as usize] {
    read_roots_with_program_id(svm, &PROGRAM_ID)
}

pub fn resolve_signers<'a>(candidates: &[&'a Keypair], extra: &[Pubkey]) -> Vec<&'a Keypair> {
    extra
        .iter()
        .map(|public_key| {
            candidates
                .iter()
                .copied()
                .find(|keypair| keypair.pubkey() == *public_key)
                .unwrap_or_else(|| {
                    panic!("extra_signers public key {public_key} not found among candidates")
                })
        })
        .collect()
}

pub fn bitmap_capacity(svm: &LiteSVM, program_id: &Pubkey, shard_id: u16) -> u32 {
    let (bitmap, _) = distributor_core::pda::find_claim_bitmap(program_id, shard_id);
    let account = svm.get_account(&bitmap).expect("bitmap account exists");
    let bitmap = distributor::state::ClaimBitmap::try_deserialize(&mut account.data.as_slice())
        .expect("bitmap account should deserialize");
    bitmap.bits.len() as u32 * 8
}
