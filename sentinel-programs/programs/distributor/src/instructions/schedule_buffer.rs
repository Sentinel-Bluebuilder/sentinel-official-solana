use anchor_lang::prelude::*;
use distributor_core::pda::SCHEDULE_BUFFER_SEED;

use crate::error::DistributorError;
pub use crate::state::MAX_SCHEDULE_BYTES;
use crate::state::ScheduleBuffer;

#[derive(Accounts)]
#[instruction(shard_id: u16, leaf_index: u32, schedule_len: u32)]
pub struct StageInit<'info> {
    #[account(mut)]
    pub rent_payer: Signer<'info>,
    #[account(
        init,
        payer = rent_payer,
        space = ScheduleBuffer::clamped_account_space(schedule_len),
        seeds = [
            SCHEDULE_BUFFER_SEED,
            &shard_id.to_le_bytes(),
            &leaf_index.to_le_bytes(),
            rent_payer.key().as_ref()
        ],
        bump
    )]
    pub schedule_buffer: Account<'info, ScheduleBuffer>,
    pub system_program: Program<'info, System>,
}

pub fn stage_init(
    ctx: Context<StageInit>,
    _shard_id: u16,
    _leaf_index: u32,
    schedule_len: u32,
) -> Result<()> {
    require!(
        schedule_len as usize <= MAX_SCHEDULE_BYTES,
        DistributorError::BufferTooLarge
    );
    let buf = &mut ctx.accounts.schedule_buffer;
    buf.rent_payer = ctx.accounts.rent_payer.key();
    buf.schedule_bytes = vec![0u8; schedule_len as usize];
    buf.finalized = false;
    buf.bump = ctx.bumps.schedule_buffer;
    Ok(())
}

#[derive(Accounts)]
#[instruction(shard_id: u16, leaf_index: u32)]
pub struct StageWrite<'info> {
    pub rent_payer: Signer<'info>,
    #[account(
        mut,
        seeds = [
            SCHEDULE_BUFFER_SEED,
            &shard_id.to_le_bytes(),
            &leaf_index.to_le_bytes(),
            rent_payer.key().as_ref()
        ],
        bump = schedule_buffer.bump
    )]
    pub schedule_buffer: Account<'info, ScheduleBuffer>,
}

pub fn stage_write(
    ctx: Context<StageWrite>,
    _shard_id: u16,
    _leaf_index: u32,
    offset: u32,
    bytes: Vec<u8>,
) -> Result<()> {
    let buf = &mut ctx.accounts.schedule_buffer;
    require!(!buf.finalized, DistributorError::BufferAlreadyFinalized);
    let start = offset as usize;
    let end = start
        .checked_add(bytes.len())
        .ok_or_else(|| error!(DistributorError::BufferOutOfRange))?;
    require!(
        end <= buf.schedule_bytes.len(),
        DistributorError::BufferOutOfRange
    );
    #[allow(clippy::indexing_slicing)]
    buf.schedule_bytes[start..end].copy_from_slice(&bytes);
    Ok(())
}

#[derive(Accounts)]
#[instruction(shard_id: u16, leaf_index: u32)]
pub struct StageFinalize<'info> {
    pub rent_payer: Signer<'info>,
    #[account(
        mut,
        seeds = [
            SCHEDULE_BUFFER_SEED,
            &shard_id.to_le_bytes(),
            &leaf_index.to_le_bytes(),
            rent_payer.key().as_ref()
        ],
        bump = schedule_buffer.bump
    )]
    pub schedule_buffer: Account<'info, ScheduleBuffer>,
}

pub fn stage_finalize(
    ctx: Context<StageFinalize>,
    _shard_id: u16,
    _leaf_index: u32,
    digest: [u8; 32],
) -> Result<()> {
    let buf = &mut ctx.accounts.schedule_buffer;
    require!(!buf.finalized, DistributorError::BufferAlreadyFinalized);
    require!(
        distributor_core::hash::keccak256(&[&buf.schedule_bytes]) == digest,
        DistributorError::BufferDigestMismatch
    );
    buf.finalized = true;
    Ok(())
}

#[derive(Accounts)]
#[instruction(shard_id: u16, leaf_index: u32)]
pub struct StageAbandon<'info> {
    #[account(mut)]
    pub rent_payer: Signer<'info>,
    #[account(
        mut,
        close = rent_payer,
        seeds = [
            SCHEDULE_BUFFER_SEED,
            &shard_id.to_le_bytes(),
            &leaf_index.to_le_bytes(),
            rent_payer.key().as_ref()
        ],
        bump = schedule_buffer.bump
    )]
    pub schedule_buffer: Account<'info, ScheduleBuffer>,
}

pub fn stage_abandon(_ctx: Context<StageAbandon>, _shard_id: u16, _leaf_index: u32) -> Result<()> {
    Ok(())
}
