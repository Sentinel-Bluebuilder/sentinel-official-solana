use anchor_lang::prelude::*;

#[account]
#[derive(InitSpace)]
pub struct NodeUnbonding {
    pub node_id: [u8; 32],
    pub owner: Pubkey,
    pub bond: u64,
    /// Unix seconds.
    pub closed_at: i64,
    pub rent_recipient: Pubkey,
    pub bump: u8,
    pub reserved: [u8; 32],
}

impl NodeUnbonding {
    pub const LEN: usize = 8 + 32 + 32 + 8 + 8 + 32 + 1 + 32;
}

const _: () = assert!(NodeUnbonding::LEN == 8 + NodeUnbonding::INIT_SPACE);
