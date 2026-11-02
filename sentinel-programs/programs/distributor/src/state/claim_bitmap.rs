use anchor_lang::prelude::*;

#[account]
pub struct ClaimBitmap {
    pub shard_id: u16,
    pub bits: Vec<u8>,
    pub bump: u8,
}

const _: () = assert!(distributor_merkle::sharded::MAX_SHARD_LEAF_COUNT <= u32::MAX as usize);

impl ClaimBitmap {
    #[allow(clippy::arithmetic_side_effects)]
    #[must_use]
    pub fn account_space(leaf_count: u32) -> usize {
        8 + 2 + 4 + (leaf_count as usize).div_ceil(8) + 1
    }

    #[allow(clippy::cast_possible_truncation)]
    #[must_use]
    pub fn clamped_account_space(leaf_count: u32) -> usize {
        Self::account_space(
            leaf_count.min(distributor_merkle::sharded::MAX_SHARD_LEAF_COUNT as u32),
        )
    }
}
