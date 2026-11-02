use anchor_lang::prelude::*;
use anchor_spl::token_2022;
use anchor_spl::token_interface::{Mint, TokenAccount, TokenInterface};
use dvpn_core::constants::PAUSE_SUBSCRIPTION_OPENS;
use dvpn_core::enums::{ListingStatus, SubscriptionCloseReason};
use dvpn_core::permissions::{
    PERM_CLOSE_FREE_SUBSCRIPTION, PERM_CLOSE_SUBSCRIPTION, PERM_OPEN_FREE_SUBSCRIPTION,
    PERM_OPEN_SUBSCRIPTION,
};
use dvpn_core::seeds::{CONFIG_SEED, SUBSCRIPTION_SEED};

use crate::error::DvpnError;
use crate::events::{SubscriptionClosed, SubscriptionOpened};
use crate::state::{Config, Grant, Plan, Provider, Subscription};

#[derive(Accounts)]
pub struct OpenSubscription<'info> {
    pub signer: Signer<'info>,
    #[account(mut)]
    pub rent_payer: Signer<'info>,
    #[doc = "CHECK: seed-only account; the priced branch authorizes `signer` against it in the handler."]
    pub owner: UncheckedAccount<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,
    #[account(has_one = provider)]
    pub plan: Account<'info, Plan>,
    #[account(mut)]
    pub provider: Box<Account<'info, Provider>>,
    #[account(
        init,
        payer = rent_payer,
        space = 8 + Subscription::INIT_SPACE,
        seeds = [SUBSCRIPTION_SEED, owner.key().as_ref(), plan.key().as_ref()],
        bump
    )]
    pub subscription: Account<'info, Subscription>,
    #[account(mut)]
    pub grant: Option<Account<'info, Grant>>,
    #[account(mut, token::mint = config.mint, token::authority = owner)]
    pub owner_token: Option<InterfaceAccount<'info, TokenAccount>>,
    #[account(mut, address = config.vault)]
    pub vault: Option<InterfaceAccount<'info, TokenAccount>>,
    #[account(address = config.mint)]
    pub mint: Option<InterfaceAccount<'info, Mint>>,
    #[account(address = token_2022::ID)]
    pub token_program: Option<Interface<'info, TokenInterface>>,
    pub system_program: Program<'info, System>,
}

pub fn open_subscription(
    ctx: Context<OpenSubscription>,
    expected_price: u64,
    expected_duration_seconds: u32,
    expected_data_cap_gb: u32,
    expected_max_connections: u8,
    subscription_rent_recipient: Pubkey,
) -> Result<()> {
    require!(
        ctx.accounts.config.paused_mask & PAUSE_SUBSCRIPTION_OPENS == 0,
        DvpnError::OperationPaused
    );
    let plan = &ctx.accounts.plan;
    require!(
        plan.status == ListingStatus::Listed as u8,
        DvpnError::PlanNotListed
    );
    require!(
        ctx.accounts.provider.status == ListingStatus::Listed as u8,
        DvpnError::ProviderNotListed
    );
    require!(
        plan.price == expected_price
            && plan.duration_seconds == expected_duration_seconds
            && plan.data_cap_gb == expected_data_cap_gb
            && plan.max_connections == expected_max_connections,
        DvpnError::PlanTermsChanged
    );
    let now = Clock::get()?.unix_timestamp;
    let signer = ctx.accounts.signer.key();
    let plan_key = ctx.accounts.plan.key();
    let provider_key = ctx.accounts.provider.key();
    let subscription_key = ctx.accounts.subscription.key();
    let plan_price = plan.price;
    let duration_seconds = plan.duration_seconds;
    let data_cap_gb = plan.data_cap_gb;
    let max_connections = plan.max_connections;

    let (price, fee) = if plan_price > 0 {
        crate::auth::require_owner_or_grantee(
            &ctx.accounts.owner.key(),
            &signer,
            ctx.accounts.grant.as_ref(),
            PERM_OPEN_SUBSCRIPTION,
            &[plan_key, provider_key, subscription_key],
            now,
        )?;
        if let Some(grant) = ctx.accounts.grant.as_mut() {
            crate::auth::charge_allowance(&mut grant.allowance, plan_price)?;
        }
        let owner_token = ctx
            .accounts
            .owner_token
            .as_ref()
            .ok_or_else(|| error!(ErrorCode::ConstraintAccountIsNone))?;
        let vault = ctx
            .accounts
            .vault
            .as_ref()
            .ok_or_else(|| error!(ErrorCode::ConstraintAccountIsNone))?;
        let mint = ctx
            .accounts
            .mint
            .as_ref()
            .ok_or_else(|| error!(ErrorCode::ConstraintAccountIsNone))?;
        let token_program = ctx
            .accounts
            .token_program
            .as_ref()
            .ok_or_else(|| error!(ErrorCode::ConstraintAccountIsNone))?;
        let fee = crate::fee::fee_amount(plan_price, ctx.accounts.config.fee_bps)?;
        if ctx.accounts.grant.is_some() {
            crate::transfer::transfer_to_vault_as_delegate(
                token_program,
                owner_token,
                mint,
                vault,
                &ctx.accounts.config.to_account_info(),
                ctx.accounts.config.bump,
                plan_price,
            )?;
        } else {
            crate::transfer::transfer_to_vault_as_owner(
                token_program,
                owner_token,
                mint,
                vault,
                &ctx.accounts.signer.to_account_info(),
                plan_price,
            )?;
        }
        let provider = &mut ctx.accounts.provider;
        let net = plan_price
            .checked_sub(fee)
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        provider.revenue = provider
            .revenue
            .checked_add(net)
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        provider.fees_accrued = provider
            .fees_accrued
            .checked_add(fee)
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        (plan_price, fee)
    } else {
        crate::auth::require_owner_or_grantee(
            &ctx.accounts.provider.owner,
            &signer,
            ctx.accounts.grant.as_ref(),
            PERM_OPEN_FREE_SUBSCRIPTION,
            &[provider_key, plan_key],
            now,
        )?;
        (0, 0)
    };
    crate::rent::require_rent_recipient(
        &subscription_rent_recipient,
        &signer,
        &ctx.accounts.rent_payer.key(),
    )?;

    let subscription = &mut ctx.accounts.subscription;
    subscription.owner = ctx.accounts.owner.key();
    subscription.plan = ctx.accounts.plan.key();
    subscription.provider = ctx.accounts.provider.key();
    subscription.rent_recipient = subscription_rent_recipient;
    subscription.price = price;
    subscription.data_cap_gb = data_cap_gb;
    subscription.max_connections = max_connections;
    subscription.opened_at = now;
    subscription.expires_at = now
        .checked_add(i64::from(duration_seconds))
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
    subscription.bump = ctx.bumps.subscription;
    subscription.reserved = [0u8; 32];
    let subscription = &ctx.accounts.subscription;
    emit!(SubscriptionOpened {
        subscription: subscription.key(),
        owner: subscription.owner,
        plan: subscription.plan,
        provider: subscription.provider,
        price,
        fee,
        expires_at: subscription.expires_at,
        data_cap_gb: subscription.data_cap_gb,
        max_connections: subscription.max_connections,
        subscription_rent_recipient: subscription.rent_recipient,
        provider_revenue: ctx.accounts.provider.revenue,
        provider_fees_accrued: ctx.accounts.provider.fees_accrued,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct CloseSubscription<'info> {
    pub signer: Signer<'info>,
    #[account(
        mut,
        close = rent_recipient,
        seeds = [SUBSCRIPTION_SEED, subscription.owner.as_ref(), subscription.plan.as_ref()],
        bump = subscription.bump
    )]
    pub subscription: Account<'info, Subscription>,
    #[doc = "CHECK: pinned to the stored rent recipient."]
    #[account(mut, address = subscription.rent_recipient)]
    pub rent_recipient: UncheckedAccount<'info>,
    pub provider: Option<Account<'info, Provider>>,
    pub grant: Option<Account<'info, Grant>>,
}

pub fn close_subscription(ctx: Context<CloseSubscription>) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    let subscription = &ctx.accounts.subscription;
    let signer = ctx.accounts.signer.key();
    let early = now < subscription.expires_at;
    let targets = [subscription.key(), subscription.plan, subscription.provider];
    let close_reason = if signer == subscription.owner {
        require!(ctx.accounts.grant.is_none(), DvpnError::Unauthorized);
        SubscriptionCloseReason::Owner
    } else if now >= subscription.expires_at {
        SubscriptionCloseReason::Expired
    } else if ctx
        .accounts
        .grant
        .as_ref()
        .is_some_and(|grant| grant.granter == subscription.owner)
    {
        crate::auth::require_owner_or_grantee(
            &subscription.owner,
            &signer,
            ctx.accounts.grant.as_ref(),
            PERM_CLOSE_SUBSCRIPTION,
            &targets,
            now,
        )?;
        SubscriptionCloseReason::Grantee
    } else if let Some(provider) = ctx.accounts.provider.as_ref() {
        require_keys_eq!(
            provider.key(),
            subscription.provider,
            DvpnError::Unauthorized
        );
        crate::auth::require_owner_or_grantee(
            &provider.owner,
            &signer,
            ctx.accounts.grant.as_ref(),
            PERM_CLOSE_FREE_SUBSCRIPTION,
            &targets,
            now,
        )?;
        require!(subscription.price == 0, DvpnError::SubscriptionPriceNotZero);
        SubscriptionCloseReason::Provider
    } else {
        return err!(DvpnError::SubscriptionNotExpired);
    };
    emit!(SubscriptionClosed {
        subscription: subscription.key(),
        owner: subscription.owner,
        plan: subscription.plan,
        provider: subscription.provider,
        closer: signer,
        close_reason: close_reason as u8,
        early,
    });
    Ok(())
}
