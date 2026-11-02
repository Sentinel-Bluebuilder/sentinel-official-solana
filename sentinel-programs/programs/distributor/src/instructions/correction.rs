use anchor_lang::prelude::*;
use anchor_spl::token_interface::{Mint, TokenAccount, TokenInterface};
use distributor_core::consts::SHARD_COUNT;
use distributor_core::pda::{CLAIM_BITMAP_SEED, CONFIG_SEED, VAULT_AUTHORITY_SEED};

use crate::error::DistributorError;
use crate::program;
use crate::state::{ClaimBitmap, Config};
use crate::transfer::vault_transfer;

#[derive(Accounts)]
pub struct SetTotalClaimable<'info> {
    pub upgrade_authority: Signer<'info>,
    #[account(constraint = program.programdata_address()? == Some(program_data.key()) @ DistributorError::Unauthorized)]
    pub program: Program<'info, program::Distributor>,
    #[account(constraint = program_data.upgrade_authority_address == Some(upgrade_authority.key()) @ DistributorError::Unauthorized)]
    pub program_data: Account<'info, ProgramData>,
    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump, constraint = !config.finalized @ DistributorError::ConfigAlreadyFinalized)]
    pub config: Account<'info, Config>,
}

pub fn set_total_claimable(ctx: Context<SetTotalClaimable>, total_claimable: u64) -> Result<()> {
    ctx.accounts.config.total_claimable = total_claimable;
    Ok(())
}

#[derive(Accounts)]
#[instruction(shard_id: u16)]
pub struct CloseBitmap<'info> {
    #[account(mut)]
    pub upgrade_authority: Signer<'info>,
    #[account(constraint = program.programdata_address()? == Some(program_data.key()) @ DistributorError::Unauthorized)]
    pub program: Program<'info, program::Distributor>,
    #[account(constraint = program_data.upgrade_authority_address == Some(upgrade_authority.key()) @ DistributorError::Unauthorized)]
    pub program_data: Account<'info, ProgramData>,
    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump, constraint = !config.finalized @ DistributorError::ConfigAlreadyFinalized)]
    pub config: Account<'info, Config>,
    #[account(
        mut,
        close = upgrade_authority,
        seeds = [CLAIM_BITMAP_SEED, &shard_id.to_le_bytes()],
        bump = bitmap.bump,
        constraint = bitmap.shard_id == shard_id @ DistributorError::ShardIdMismatch
    )]
    pub bitmap: Account<'info, ClaimBitmap>,
}

pub fn close_bitmap(ctx: Context<CloseBitmap>, shard_id: u16) -> Result<()> {
    require!(shard_id < SHARD_COUNT, DistributorError::ShardIdOutOfRange);
    let config = &mut ctx.accounts.config;
    config.bitmap_count = config
        .bitmap_count
        .checked_sub(1)
        .ok_or_else(|| error!(DistributorError::BitmapCountMismatch))?;
    #[allow(clippy::indexing_slicing)]
    {
        config.leaf_counts[shard_id as usize] = 0;
    }
    Ok(())
}

#[derive(Accounts)]
pub struct SweepVaultExcess<'info> {
    pub upgrade_authority: Signer<'info>,
    #[account(constraint = program.programdata_address()? == Some(program_data.key()) @ DistributorError::Unauthorized)]
    pub program: Program<'info, program::Distributor>,
    #[account(constraint = program_data.upgrade_authority_address == Some(upgrade_authority.key()) @ DistributorError::Unauthorized)]
    pub program_data: Account<'info, ProgramData>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump, constraint = !config.finalized @ DistributorError::ConfigAlreadyFinalized)]
    pub config: Account<'info, Config>,
    #[account(mut, address = config.vault)]
    pub vault: InterfaceAccount<'info, TokenAccount>,
    #[account(mut, address = config.treasury)]
    pub treasury: InterfaceAccount<'info, TokenAccount>,
    #[account(address = config.mint)]
    pub mint: InterfaceAccount<'info, Mint>,
    #[doc = "CHECK: vault authority PDA; bump from config."]
    #[account(seeds = [VAULT_AUTHORITY_SEED], bump = config.vault_authority_bump)]
    pub vault_authority: UncheckedAccount<'info>,
    #[account(address = config.token_program)]
    pub token_program: Interface<'info, TokenInterface>,
}

pub fn sweep_vault_excess(ctx: Context<SweepVaultExcess>) -> Result<()> {
    let excess = ctx
        .accounts
        .vault
        .amount
        .checked_sub(ctx.accounts.config.total_claimable)
        .ok_or_else(|| error!(DistributorError::VaultExcessZero))?;
    require!(excess > 0, DistributorError::VaultExcessZero);
    vault_transfer(
        &ctx.accounts.token_program,
        &ctx.accounts.vault,
        &ctx.accounts.mint,
        &ctx.accounts.treasury,
        &ctx.accounts.vault_authority.to_account_info(),
        ctx.accounts.config.vault_authority_bump,
        excess,
    )
}
