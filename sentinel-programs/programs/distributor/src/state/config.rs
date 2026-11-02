use anchor_lang::prelude::*;
use distributor_core::consts::SHARD_COUNT;

#[account]
#[derive(InitSpace)]
pub struct Config {
    pub upgrade_authority: Pubkey,
    pub mint: Pubkey,
    pub treasury: Pubkey,
    pub vault: Pubkey,
    pub vault_authority_bump: u8,
    pub roots_commitment: [u8; 32],
    pub total_claimable: u64,
    pub bitmap_count: u16,
    pub finalized: bool,
    pub leaf_counts: [u32; SHARD_COUNT as usize],
    pub bump: u8,
    pub token_program: Pubkey,
}

impl Config {
    pub const LEN: usize =
        8 + 32 + 32 + 32 + 32 + 1 + 32 + 8 + 2 + 1 + 4 * (SHARD_COUNT as usize) + 1 + 32;
}

const _: () = assert!(Config::LEN == 8 + Config::INIT_SPACE);
