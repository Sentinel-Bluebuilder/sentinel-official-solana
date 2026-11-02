use anchor_lang::prelude::*;

#[account]
#[derive(InitSpace)]
pub struct Channel {
    pub channel_id: [u8; 32],
    pub owner: Pubkey,
    pub node: Pubkey,
    pub opener: Pubkey,
    pub mode: u8,
    pub voucher_signer: Pubkey,
    pub rate_per_hour: u64,
    pub stream_base: u64,
    /// Unix seconds.
    pub stream_started_at: i64,
    pub escrow: u64,
    pub redeemed: u64,
    /// Unix seconds.
    pub opened_at: i64,
    /// Unix seconds.
    pub close_started_at: i64,
    pub status: u8,
    pub rent_recipient: Pubkey,
    /// Unix seconds.
    pub node_opened_at: i64,
    pub bump: u8,
    pub reserved: [u8; 32],
}

impl Channel {
    pub const LEN: usize =
        8 + 32 + 32 + 32 + 32 + 1 + 32 + 8 + 8 + 8 + 8 + 8 + 8 + 8 + 1 + 32 + 8 + 1 + 32;
}

const _: () = assert!(Channel::LEN == 8 + Channel::INIT_SPACE);
