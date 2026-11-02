use anchor_lang::prelude::*;
use distributor_core::consts::SHARD_COUNT;

#[account(zero_copy)]
#[repr(C)]
pub struct Roots {
    pub shard_roots: [[u8; 32]; SHARD_COUNT as usize],
}

impl Roots {
    pub const LEN: usize = 8 + size_of::<Roots>();
}

const _: () = assert!(Roots::LEN == 4104);
