use anchor_lang::prelude::*;

#[account]
#[derive(InitSpace)]
pub struct Plan {
    pub plan_id: [u8; 32],
    pub provider: Pubkey,
    pub price: u64,
    pub duration_seconds: u32,
    pub data_cap_gb: u32,
    pub max_connections: u8,
    pub status: u8,
    pub rent_recipient: Pubkey,
    pub bump: u8,
    pub reserved: [u8; 32],
}

impl Plan {
    pub const LEN: usize = 8 + 32 + 32 + 8 + 4 + 4 + 1 + 1 + 32 + 1 + 32;
}

const _: () = assert!(Plan::LEN == 8 + Plan::INIT_SPACE);
