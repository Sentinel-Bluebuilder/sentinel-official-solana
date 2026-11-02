use anchor_lang::prelude::*;

#[account]
#[derive(InitSpace)]
pub struct Config {
    pub authority: Pubkey,
    pub pending_authority: Pubkey,
    pub mint: Pubkey,
    pub decimals: u8,
    pub vault: Pubkey,
    pub treasury: Pubkey,
    pub fee_bps: u16,
    pub min_node_bond: u64,
    pub min_provider_bond: u64,
    pub unbonding_seconds: u32,
    pub channel_close_grace_seconds: u32,
    pub lease_close_grace_seconds: u32,
    pub node_listing_ttl_seconds: u32,
    pub paused_mask: u8,
    pub bump: u8,
    pub fees_accrued: u64,
    pub reserved: [u8; 64],
}

impl Config {
    pub const LEN: usize =
        8 + 32 + 32 + 32 + 1 + 32 + 32 + 2 + 8 + 8 + 4 + 4 + 4 + 4 + 1 + 1 + 8 + 64;
}

const _: () = assert!(Config::LEN == 8 + Config::INIT_SPACE);
