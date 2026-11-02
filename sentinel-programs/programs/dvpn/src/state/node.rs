use anchor_lang::prelude::*;

#[account]
#[derive(InitSpace)]
pub struct Node {
    pub node_id: [u8; 32],
    pub owner: Pubkey,
    pub status: u8,
    /// Unix seconds.
    pub listed_at: i64,
    pub channel_price_per_gb: u64,
    pub channel_price_per_hour: u64,
    pub lease_price_per_hour: u64,
    pub protocols: u32,
    pub country: [u8; 2],
    pub uri_len: u8,
    pub uri: [u8; 128],
    pub bond: u64,
    pub revenue: u64,
    pub fees_accrued: u64,
    pub rent_recipient: Pubkey,
    /// Unix seconds.
    pub opened_at: i64,
    pub bump: u8,
    pub reserved: [u8; 32],
}

impl Node {
    pub const LEN: usize =
        8 + 32 + 32 + 1 + 8 + 8 + 8 + 8 + 4 + 2 + 1 + 128 + 8 + 8 + 8 + 32 + 8 + 1 + 32;
}

const _: () = assert!(Node::LEN == 8 + Node::INIT_SPACE);
