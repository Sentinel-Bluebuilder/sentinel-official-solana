use anchor_lang::prelude::*;
use anchor_spl::token_2022;
use anchor_spl::token_interface::{Mint, TokenAccount, TokenInterface};
use dvpn_core::permissions::PERM_WITHDRAW_USER_BALANCE;
use dvpn_core::seeds::{CONFIG_SEED, GRANT_SEED, USER_SEED};

use crate::error::DvpnError;
use crate::events::UserBalanceWithdrawn;
use crate::state::{Config, Grant, User};

#[derive(Accounts)]
pub struct WithdrawUserBalance<'info> {
    pub signer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[doc = "CHECK: the wallet the balance belongs to; the grant gate validates it against `signer`."]
    pub owner: UncheckedAccount<'info>,
    #[account(seeds = [GRANT_SEED, owner.key().as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
    #[account(mut, seeds = [USER_SEED, owner.key().as_ref()], bump = user.bump)]
    pub user: Account<'info, User>,
    #[doc = "CHECK: pinned to the stored rent recipient."]
    #[account(mut, address = user.rent_recipient)]
    pub rent_recipient: UncheckedAccount<'info>,
    #[account(mut, token::mint = config.mint, token::authority = owner)]
    pub owner_token: InterfaceAccount<'info, TokenAccount>,
    #[account(mut, address = config.vault)]
    pub vault: InterfaceAccount<'info, TokenAccount>,
    #[account(address = config.mint)]
    pub mint: InterfaceAccount<'info, Mint>,
    #[account(address = token_2022::ID)]
    pub token_program: Interface<'info, TokenInterface>,
}

pub fn withdraw_user_balance(ctx: Context<WithdrawUserBalance>) -> Result<()> {
    crate::auth::require_owner_or_grantee(
        &ctx.accounts.owner.key(),
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_WITHDRAW_USER_BALANCE,
        &[],
        Clock::get()?.unix_timestamp,
    )?;
    let balance = ctx.accounts.user.balance;
    let open_channels = ctx.accounts.user.open_channels;
    require!(balance > 0 || open_channels == 0, DvpnError::BalanceZero);
    if balance > 0 {
        crate::transfer::transfer_from_vault(
            &ctx.accounts.token_program,
            &ctx.accounts.vault,
            &ctx.accounts.mint,
            &ctx.accounts.owner_token,
            &ctx.accounts.config.to_account_info(),
            ctx.accounts.config.bump,
            balance,
        )?;
        ctx.accounts.user.balance = 0;
    }
    let closed = open_channels == 0;
    let key = ctx.accounts.user.key();
    if closed {
        ctx.accounts
            .user
            .close(ctx.accounts.rent_recipient.to_account_info())?;
    }
    emit!(UserBalanceWithdrawn {
        user: key,
        owner: ctx.accounts.owner.key(),
        destination: ctx.accounts.owner_token.key(),
        amount: balance,
        closed,
    });
    Ok(())
}
