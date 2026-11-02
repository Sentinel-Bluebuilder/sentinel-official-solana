use anchor_lang::prelude::*;

#[account]
#[derive(InitSpace)]
pub struct Subscription {
    pub owner: Pubkey,
    pub plan: Pubkey,
    pub provider: Pubkey,
    pub rent_recipient: Pubkey,
    pub price: u64,
    /// Unix seconds.
    pub opened_at: i64,
    /// Unix seconds.
    pub expires_at: i64,
    pub data_cap_gb: u32,
    pub max_connections: u8,
    pub bump: u8,
    pub reserved: [u8; 32],
}

impl Subscription {
    pub const LEN: usize = 8 + 32 + 32 + 32 + 32 + 8 + 8 + 8 + 4 + 1 + 1 + 32;
}

const _: () = assert!(Subscription::LEN == 8 + Subscription::INIT_SPACE);
