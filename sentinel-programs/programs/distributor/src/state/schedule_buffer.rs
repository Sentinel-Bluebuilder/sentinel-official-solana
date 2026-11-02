use anchor_lang::prelude::*;

#[account]
pub struct ScheduleBuffer {
    pub rent_payer: Pubkey,
    pub schedule_bytes: Vec<u8>,
    pub finalized: bool,
    pub bump: u8,
}

pub const MAX_SCHEDULE_BYTES: usize = 29 + distributor_vesting::unlock::MAX_PERIOD_COUNT * 16;

const _: () = assert!(MAX_SCHEDULE_BYTES <= u32::MAX as usize);

impl ScheduleBuffer {
    #[allow(clippy::arithmetic_side_effects)]
    #[must_use]
    pub fn account_space(schedule_len: u32) -> usize {
        8 + 32 + (4 + schedule_len as usize) + 1 + 1
    }

    #[allow(clippy::cast_possible_truncation)]
    #[must_use]
    pub fn clamped_account_space(schedule_len: u32) -> usize {
        Self::account_space(schedule_len.min(MAX_SCHEDULE_BYTES as u32))
    }
}
