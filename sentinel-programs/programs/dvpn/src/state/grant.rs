use anchor_lang::prelude::*;

#[account]
#[derive(InitSpace)]
pub struct Grant {
    pub granter: Pubkey,
    pub grantee: Pubkey,
    pub target: Pubkey,
    pub permissions: u64,
    /// Unix seconds.
    pub expires_at: i64,
    pub allowance: u64,
    pub rent_recipient: Pubkey,
    /// Unix seconds.
    pub created_at: i64,
    pub bump: u8,
    pub reserved: [u8; 32],
}

impl Grant {
    pub const LEN: usize = 8 + 32 + 32 + 32 + 8 + 8 + 8 + 32 + 8 + 1 + 32;

    #[must_use]
    pub fn is_expired(&self, now: i64) -> bool {
        self.expires_at != 0 && now >= self.expires_at
    }
}

const _: () = assert!(Grant::LEN == 8 + Grant::INIT_SPACE);
