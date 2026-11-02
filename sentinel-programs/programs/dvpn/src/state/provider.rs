use anchor_lang::prelude::*;

#[account]
#[derive(InitSpace)]
pub struct Provider {
    pub provider_id: [u8; 32],
    pub owner: Pubkey,
    pub status: u8,
    pub uri_len: u8,
    pub uri: [u8; 128],
    pub bond: u64,
    pub revenue: u64,
    pub fees_accrued: u64,
    pub balance: u64,
    pub open_plans: u16,
    pub open_leases: u32,
    pub rent_recipient: Pubkey,
    /// Unix seconds.
    pub opened_at: i64,
    pub bump: u8,
    pub reserved: [u8; 32],
}

impl Provider {
    pub const LEN: usize = 8 + 32 + 32 + 1 + 1 + 128 + 8 + 8 + 8 + 8 + 2 + 4 + 32 + 8 + 1 + 32;
}

const _: () = assert!(Provider::LEN == 8 + Provider::INIT_SPACE);
