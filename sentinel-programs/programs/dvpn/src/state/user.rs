use anchor_lang::prelude::*;

#[account]
#[derive(InitSpace)]
pub struct User {
    pub owner: Pubkey,
    pub balance: u64,
    pub open_channels: u32,
    pub rent_recipient: Pubkey,
    pub bump: u8,
    pub reserved: [u8; 32],
}

impl User {
    pub const LEN: usize = 8 + 32 + 8 + 4 + 32 + 1 + 32;
}

const _: () = assert!(User::LEN == 8 + User::INIT_SPACE);
