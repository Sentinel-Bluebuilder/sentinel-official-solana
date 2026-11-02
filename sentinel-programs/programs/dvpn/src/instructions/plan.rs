use anchor_lang::prelude::*;
use dvpn_core::constants::PAUSE_PLAN_OPENS;
use dvpn_core::enums::ListingStatus;
use dvpn_core::permissions::{
    PERM_CLOSE_PLAN, PERM_DELIST_PLAN, PERM_LIST_PLAN, PERM_OPEN_PLAN, PERM_UPDATE_PLAN,
};
use dvpn_core::seeds::{CONFIG_SEED, GRANT_SEED, PLAN_SEED, PROVIDER_SEED};

use crate::error::DvpnError;
use crate::events::{PlanClosed, PlanDelisted, PlanListed, PlanOpened, PlanUpdated};
use crate::state::{Config, Grant, Plan, Provider};

#[derive(Accounts)]
#[instruction(plan_id: [u8; 32])]
pub struct OpenPlan<'info> {
    pub signer: Signer<'info>,
    #[account(mut)]
    pub rent_payer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(
        mut,
        seeds = [PROVIDER_SEED, provider.owner.as_ref(), provider.provider_id.as_ref()],
        bump = provider.bump
    )]
    pub provider: Account<'info, Provider>,
    #[account(seeds = [GRANT_SEED, provider.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
    #[account(
        init,
        payer = rent_payer,
        space = 8 + Plan::INIT_SPACE,
        seeds = [PLAN_SEED, provider.key().as_ref(), plan_id.as_ref()],
        bump
    )]
    pub plan: Account<'info, Plan>,
    pub system_program: Program<'info, System>,
}

pub fn open_plan(
    ctx: Context<OpenPlan>,
    plan_id: [u8; 32],
    price: u64,
    duration_seconds: u32,
    data_cap_gb: u32,
    max_connections: u8,
    plan_rent_recipient: Pubkey,
) -> Result<()> {
    require!(
        ctx.accounts.config.paused_mask & PAUSE_PLAN_OPENS == 0,
        DvpnError::OperationPaused
    );
    let now = Clock::get()?.unix_timestamp;
    crate::auth::require_owner_or_grantee(
        &ctx.accounts.provider.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_OPEN_PLAN,
        &[ctx.accounts.provider.key(), ctx.accounts.plan.key()],
        now,
    )?;
    crate::rent::require_rent_recipient(
        &plan_rent_recipient,
        &ctx.accounts.signer.key(),
        &ctx.accounts.rent_payer.key(),
    )?;
    require!(duration_seconds > 0, DvpnError::ParamOutOfRange);
    let provider_key = ctx.accounts.provider.key();
    let plan_key = ctx.accounts.plan.key();
    let plan: &mut Plan = &mut ctx.accounts.plan;
    plan.plan_id = plan_id;
    plan.provider = provider_key;
    plan.price = price;
    plan.duration_seconds = duration_seconds;
    plan.data_cap_gb = data_cap_gb;
    plan.max_connections = max_connections;
    plan.status = ListingStatus::Delisted as u8;
    plan.rent_recipient = plan_rent_recipient;
    plan.bump = ctx.bumps.plan;
    plan.reserved = [0u8; 32];
    ctx.accounts.provider.open_plans = ctx
        .accounts
        .provider
        .open_plans
        .checked_add(1)
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
    emit!(PlanOpened {
        plan: plan_key,
        provider: provider_key,
        plan_id,
        price,
        duration_seconds,
        data_cap_gb,
        max_connections,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct UpdatePlan<'info> {
    pub signer: Signer<'info>,
    pub provider: Account<'info, Provider>,
    #[account(seeds = [GRANT_SEED, provider.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
    #[account(mut, has_one = provider)]
    pub plan: Account<'info, Plan>,
}

pub fn update_plan(
    ctx: Context<UpdatePlan>,
    price: Option<u64>,
    duration_seconds: Option<u32>,
    data_cap_gb: Option<u32>,
    max_connections: Option<u8>,
) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    crate::auth::require_owner_or_grantee(
        &ctx.accounts.provider.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_UPDATE_PLAN,
        &[ctx.accounts.provider.key(), ctx.accounts.plan.key()],
        now,
    )?;
    if let Some(duration_seconds) = duration_seconds {
        require!(duration_seconds > 0, DvpnError::ParamOutOfRange);
    }
    let plan_key = ctx.accounts.plan.key();
    let plan: &mut Plan = &mut ctx.accounts.plan;
    if let Some(price) = price {
        plan.price = price;
    }
    if let Some(duration_seconds) = duration_seconds {
        plan.duration_seconds = duration_seconds;
    }
    if let Some(data_cap_gb) = data_cap_gb {
        plan.data_cap_gb = data_cap_gb;
    }
    if let Some(max_connections) = max_connections {
        plan.max_connections = max_connections;
    }
    emit!(PlanUpdated {
        plan: plan_key,
        provider: ctx.accounts.provider.key(),
        price: plan.price,
        duration_seconds: plan.duration_seconds,
        data_cap_gb: plan.data_cap_gb,
        max_connections: plan.max_connections,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct ListPlan<'info> {
    pub signer: Signer<'info>,
    pub provider: Account<'info, Provider>,
    #[account(seeds = [GRANT_SEED, provider.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
    #[account(mut, has_one = provider)]
    pub plan: Account<'info, Plan>,
}

pub fn list_plan(ctx: Context<ListPlan>) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    crate::auth::require_owner_or_grantee(
        &ctx.accounts.provider.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_LIST_PLAN,
        &[ctx.accounts.provider.key(), ctx.accounts.plan.key()],
        now,
    )?;
    ctx.accounts.plan.status = ListingStatus::Listed as u8;
    emit!(PlanListed {
        plan: ctx.accounts.plan.key(),
    });
    Ok(())
}

#[derive(Accounts)]
pub struct DelistPlan<'info> {
    pub signer: Signer<'info>,
    pub provider: Account<'info, Provider>,
    #[account(seeds = [GRANT_SEED, provider.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
    #[account(mut, has_one = provider)]
    pub plan: Account<'info, Plan>,
}

pub fn delist_plan(ctx: Context<DelistPlan>) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    crate::auth::require_owner_or_grantee(
        &ctx.accounts.provider.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_DELIST_PLAN,
        &[ctx.accounts.provider.key(), ctx.accounts.plan.key()],
        now,
    )?;
    ctx.accounts.plan.status = ListingStatus::Delisted as u8;
    emit!(PlanDelisted {
        plan: ctx.accounts.plan.key(),
    });
    Ok(())
}

#[derive(Accounts)]
pub struct ClosePlan<'info> {
    pub signer: Signer<'info>,
    #[account(mut)]
    pub provider: Account<'info, Provider>,
    #[account(seeds = [GRANT_SEED, provider.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
    #[account(mut, close = rent_recipient, has_one = provider)]
    pub plan: Account<'info, Plan>,
    #[doc = "CHECK: receives the Plan's rent; pinned to the stored rent recipient."]
    #[account(mut, address = plan.rent_recipient)]
    pub rent_recipient: UncheckedAccount<'info>,
}

pub fn close_plan(ctx: Context<ClosePlan>) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    crate::auth::require_owner_or_grantee(
        &ctx.accounts.provider.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_CLOSE_PLAN,
        &[ctx.accounts.provider.key(), ctx.accounts.plan.key()],
        now,
    )?;
    require!(
        ctx.accounts.plan.status == ListingStatus::Delisted as u8,
        DvpnError::PlanNotDelisted
    );
    ctx.accounts.provider.open_plans = ctx
        .accounts
        .provider
        .open_plans
        .checked_sub(1)
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
    emit!(PlanClosed {
        plan: ctx.accounts.plan.key(),
        provider: ctx.accounts.provider.key(),
    });
    Ok(())
}
