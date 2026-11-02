use anchor_lang::prelude::*;

#[event]
pub struct LeafClaimed {
    pub shard_id: u16,
    pub leaf_index: u32,
    pub beneficiary: Pubkey,
    pub liquid_amount: u64,
    pub locked_amount: u64,
    pub folded: bool,
}

#[event]
pub struct VestedWithdrawn {
    pub shard_id: u16,
    pub leaf_index: u32,
    pub beneficiary: Pubkey,
    pub amount: u64,
    pub closed: bool,
}
