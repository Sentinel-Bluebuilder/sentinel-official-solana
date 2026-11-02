#![deny(
    clippy::unwrap_used,
    clippy::expect_used,
    clippy::panic,
    clippy::indexing_slicing,
    clippy::arithmetic_side_effects
)]

use anchor_lang::prelude::*;

pub mod bitmap;
pub mod error;
pub mod events;
pub mod instructions;
pub mod state;
pub mod transfer;
pub mod verify;

use instructions::*;

declare_id!("SentineLDistributor111111111111111111111111");

#[program]
pub mod distributor {
    use super::*;

    pub fn initialize_config(ctx: Context<InitializeConfig>, total_claimable: u64) -> Result<()> {
        instructions::initialize_config(ctx, total_claimable)
    }

    pub fn initialize_roots(ctx: Context<InitializeRoots>) -> Result<()> {
        instructions::initialize_roots(ctx)
    }

    pub fn write_roots_chunk(
        ctx: Context<WriteRootsChunk>,
        start_index: u16,
        chunk: Vec<[u8; 32]>,
    ) -> Result<()> {
        instructions::write_roots_chunk(ctx, start_index, chunk)
    }

    pub fn initialize_bitmap(
        ctx: Context<InitializeBitmap>,
        shard_id: u16,
        leaf_count: u32,
    ) -> Result<()> {
        instructions::initialize_bitmap(ctx, shard_id, leaf_count)
    }

    pub fn finalize_genesis(
        ctx: Context<FinalizeGenesis>,
        expected_roots_commitment: [u8; 32],
    ) -> Result<()> {
        instructions::finalize_genesis(ctx, expected_roots_commitment)
    }

    pub fn set_total_claimable(
        ctx: Context<SetTotalClaimable>,
        total_claimable: u64,
    ) -> Result<()> {
        instructions::set_total_claimable(ctx, total_claimable)
    }

    pub fn close_bitmap(ctx: Context<CloseBitmap>, shard_id: u16) -> Result<()> {
        instructions::close_bitmap(ctx, shard_id)
    }

    pub fn sweep_vault_excess(ctx: Context<SweepVaultExcess>) -> Result<()> {
        instructions::sweep_vault_excess(ctx)
    }

    pub fn claim(ctx: Context<Claim>, args: ClaimArgs) -> Result<()> {
        instructions::claim(ctx, args)
    }

    pub fn withdraw_vested(
        ctx: Context<WithdrawVested>,
        shard_id: u16,
        leaf_index: u32,
    ) -> Result<()> {
        instructions::withdraw_vested(ctx, shard_id, leaf_index)
    }

    pub fn stage_init(
        ctx: Context<StageInit>,
        shard_id: u16,
        leaf_index: u32,
        schedule_len: u32,
    ) -> Result<()> {
        instructions::stage_init(ctx, shard_id, leaf_index, schedule_len)
    }

    pub fn stage_write(
        ctx: Context<StageWrite>,
        shard_id: u16,
        leaf_index: u32,
        offset: u32,
        bytes: Vec<u8>,
    ) -> Result<()> {
        instructions::stage_write(ctx, shard_id, leaf_index, offset, bytes)
    }

    pub fn stage_finalize(
        ctx: Context<StageFinalize>,
        shard_id: u16,
        leaf_index: u32,
        digest: [u8; 32],
    ) -> Result<()> {
        instructions::stage_finalize(ctx, shard_id, leaf_index, digest)
    }

    pub fn stage_abandon(ctx: Context<StageAbandon>, shard_id: u16, leaf_index: u32) -> Result<()> {
        instructions::stage_abandon(ctx, shard_id, leaf_index)
    }
}
