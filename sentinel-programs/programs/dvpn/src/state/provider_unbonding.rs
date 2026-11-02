use anchor_lang::prelude::*;

#[account]
#[derive(InitSpace)]
pub struct ProviderUnbonding {
    pub provider_id: [u8; 32],
    pub owner: Pubkey,
    pub bond: u64,
    /// Unix seconds.
    pub closed_at: i64,
    pub rent_recipient: Pubkey,
    pub bump: u8,
    pub reserved: [u8; 32],
}

impl ProviderUnbonding {
    pub const LEN: usize = 8 + 32 + 32 + 8 + 8 + 32 + 1 + 32;
}

const _: () = assert!(ProviderUnbonding::LEN == 8 + ProviderUnbonding::INIT_SPACE);
