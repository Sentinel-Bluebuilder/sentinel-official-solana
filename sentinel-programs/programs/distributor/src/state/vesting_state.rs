use anchor_lang::prelude::*;
use distributor_vesting::schedule::Schedule;

#[account]
pub struct VestingState {
    pub beneficiary: Pubkey,
    pub bech32_address: String,
    pub compressed_public_key: [u8; 33],
    pub total: u64,
    pub withdrawn: u64,
    pub schedule: Schedule,
    pub rent_payer: Pubkey,
    pub bump: u8,
}

impl VestingState {
    #[allow(clippy::arithmetic_side_effects)]
    #[must_use]
    pub fn account_space(address_len: u32, period_count: u32) -> usize {
        let schedule_len = 1 + 8 + 8 + (4 + period_count as usize * 16) + 8;
        8 + 32 + (4 + address_len as usize) + 33 + 8 + 8 + schedule_len + 32 + 1
    }
}
