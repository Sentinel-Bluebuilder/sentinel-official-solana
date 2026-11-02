use anchor_lang::prelude::*;
use anchor_spl::associated_token::AssociatedToken;
use anchor_spl::token_interface::{Mint, TokenAccount, TokenInterface};
use distributor_core::pda::{CONFIG_SEED, VAULT_AUTHORITY_SEED, VESTING_STATE_SEED};

use crate::error::DistributorError;
use crate::events::VestedWithdrawn;
use crate::state::{Config, VestingState};
use crate::transfer::vault_transfer;

#[derive(Accounts)]
#[instruction(shard_id: u16, leaf_index: u32)]
pub struct WithdrawVested<'info> {
    #[account(mut)]
    pub payer: Signer<'info>,
    #[account(
        mut,
        seeds = [VESTING_STATE_SEED, &shard_id.to_le_bytes(), &leaf_index.to_le_bytes()],
        bump = vesting_state.bump
    )]
    pub vesting_state: Box<Account<'info, VestingState>>,
    #[doc = "CHECK: fixed to the recorded beneficiary; ATA authority."]
    #[account(address = vesting_state.beneficiary)]
    pub beneficiary: UncheckedAccount<'info>,
    #[doc = "CHECK: fixed to the recorded rent payer; receives the closed rent."]
    #[account(mut, address = vesting_state.rent_payer)]
    pub rent_recipient: UncheckedAccount<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,
    #[account(mut, address = config.vault)]
    pub vault: Box<InterfaceAccount<'info, TokenAccount>>,
    #[doc = "CHECK: vault authority PDA."]
    #[account(seeds = [VAULT_AUTHORITY_SEED], bump = config.vault_authority_bump)]
    pub vault_authority: UncheckedAccount<'info>,
    #[account(address = config.mint)]
    pub mint: Box<InterfaceAccount<'info, Mint>>,
    #[account(
        init_if_needed,
        payer = payer,
        associated_token::mint = mint,
        associated_token::authority = beneficiary,
        associated_token::token_program = token_program
    )]
    pub beneficiary_token: Box<InterfaceAccount<'info, TokenAccount>>,
    #[account(address = config.token_program)]
    pub token_program: Interface<'info, TokenInterface>,
    pub associated_token_program: Program<'info, AssociatedToken>,
    pub system_program: Program<'info, System>,
}

pub fn withdraw_vested(ctx: Context<WithdrawVested>, shard_id: u16, leaf_index: u32) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;

    let validated = distributor_vesting::unlock::check_schedule(
        &ctx.accounts.vesting_state.schedule,
        ctx.accounts.vesting_state.total,
    )
    .map_err(|_| error!(DistributorError::InvalidStoredSchedule))?;
    let unlocked = distributor_vesting::unlock::unlocked_amount(&validated, now)
        .min(ctx.accounts.vesting_state.total);
    let amount = unlocked.saturating_sub(ctx.accounts.vesting_state.withdrawn);
    require!(amount > 0, DistributorError::WithdrawableZero);

    ctx.accounts.vesting_state.withdrawn = ctx
        .accounts
        .vesting_state
        .withdrawn
        .checked_add(amount)
        .ok_or_else(|| error!(DistributorError::ArithmeticOverflow))?;

    vault_transfer(
        &ctx.accounts.token_program,
        &ctx.accounts.vault,
        &ctx.accounts.mint,
        &ctx.accounts.beneficiary_token,
        &ctx.accounts.vault_authority.to_account_info(),
        ctx.accounts.config.vault_authority_bump,
        amount,
    )?;

    let closed = ctx.accounts.vesting_state.withdrawn == ctx.accounts.vesting_state.total;
    if closed {
        ctx.accounts
            .vesting_state
            .close(ctx.accounts.rent_recipient.to_account_info())?;
    }
    emit!(VestedWithdrawn {
        shard_id,
        leaf_index,
        beneficiary: ctx.accounts.beneficiary.key(),
        amount,
        closed,
    });
    Ok(())
}
