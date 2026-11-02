use anchor_lang::prelude::*;
use anchor_spl::token_2022;
use anchor_spl::token_interface::{Mint, TokenAccount, TokenInterface};
use dvpn_core::constants::PAUSE_PROVIDER_OPENS;
use dvpn_core::enums::ListingStatus;
use dvpn_core::permissions::{
    PERM_CLOSE_PROVIDER, PERM_DELIST_PROVIDER, PERM_LIST_PROVIDER, PERM_OPEN_PROVIDER,
    PERM_UPDATE_PROVIDER, PERM_WITHDRAW_PROVIDER_BALANCE, PERM_WITHDRAW_PROVIDER_BOND,
    PERM_WITHDRAW_PROVIDER_REVENUE,
};
use dvpn_core::seeds::{CONFIG_SEED, GRANT_SEED, PROVIDER_SEED, PROVIDER_UNBONDING_SEED};

use crate::error::DvpnError;
use crate::events::{
    ProviderBalanceWithdrawn, ProviderBondWithdrawn, ProviderClosed, ProviderDelisted,
    ProviderFeesSwept, ProviderListed, ProviderOpened, ProviderRevenueWithdrawn, ProviderUpdated,
};
use crate::state::{Config, Grant, Provider, ProviderUnbonding};

#[derive(Accounts)]
#[instruction(provider_id: [u8; 32])]
pub struct OpenProvider<'info> {
    pub signer: Signer<'info>,
    #[account(mut)]
    pub rent_payer: Signer<'info>,
    #[doc = "CHECK: the wallet the provider belongs to; the grant gate validates it against `signer`."]
    pub owner: UncheckedAccount<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(mut, seeds = [GRANT_SEED, owner.key().as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
    #[account(
        init,
        payer = rent_payer,
        space = 8 + Provider::INIT_SPACE,
        seeds = [PROVIDER_SEED, owner.key().as_ref(), provider_id.as_ref()],
        bump
    )]
    pub provider: Account<'info, Provider>,
    #[doc = "CHECK: parked-bond address; must be System-owned with no data (BondPending otherwise)."]
    #[account(seeds = [PROVIDER_UNBONDING_SEED, owner.key().as_ref(), provider_id.as_ref()], bump)]
    pub provider_unbonding: UncheckedAccount<'info>,
    #[account(mut, token::mint = config.mint, token::authority = owner)]
    pub owner_token: InterfaceAccount<'info, TokenAccount>,
    #[account(mut, address = config.vault)]
    pub vault: InterfaceAccount<'info, TokenAccount>,
    #[account(address = config.mint)]
    pub mint: InterfaceAccount<'info, Mint>,
    #[account(address = token_2022::ID)]
    pub token_program: Interface<'info, TokenInterface>,
    pub system_program: Program<'info, System>,
}

pub fn open_provider(
    ctx: Context<OpenProvider>,
    provider_id: [u8; 32],
    uri: Vec<u8>,
    bond: u64,
    provider_rent_recipient: Pubkey,
) -> Result<()> {
    require!(
        ctx.accounts.config.paused_mask & PAUSE_PROVIDER_OPENS == 0,
        DvpnError::OperationPaused
    );
    crate::unbonding::require_no_pending_unbonding(&ctx.accounts.provider_unbonding)?;
    require!(
        bond >= ctx.accounts.config.min_provider_bond,
        DvpnError::BondBelowMinimum
    );
    let now = Clock::get()?.unix_timestamp;
    let owner = ctx.accounts.owner.key();
    let provider_key = ctx.accounts.provider.key();
    crate::auth::require_owner_or_grantee(
        &owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_OPEN_PROVIDER,
        &[provider_key],
        now,
    )?;
    crate::rent::require_rent_recipient(
        &provider_rent_recipient,
        &ctx.accounts.signer.key(),
        &ctx.accounts.rent_payer.key(),
    )?;
    if let Some(grant) = ctx.accounts.grant.as_mut() {
        crate::auth::charge_allowance(&mut grant.allowance, bond)?;
    }
    if bond > 0 {
        if ctx.accounts.grant.is_some() {
            crate::transfer::transfer_to_vault_as_delegate(
                &ctx.accounts.token_program,
                &ctx.accounts.owner_token,
                &ctx.accounts.mint,
                &ctx.accounts.vault,
                &ctx.accounts.config.to_account_info(),
                ctx.accounts.config.bump,
                bond,
            )?;
        } else {
            crate::transfer::transfer_to_vault_as_owner(
                &ctx.accounts.token_program,
                &ctx.accounts.owner_token,
                &ctx.accounts.mint,
                &ctx.accounts.vault,
                &ctx.accounts.signer.to_account_info(),
                bond,
            )?;
        }
    }
    let provider: &mut Provider = &mut ctx.accounts.provider;
    provider.provider_id = provider_id;
    provider.owner = owner;
    provider.status = ListingStatus::Delisted as u8;
    crate::uri::write_uri(&mut provider.uri, &mut provider.uri_len, &uri)?;
    provider.bond = bond;
    provider.revenue = 0;
    provider.fees_accrued = 0;
    provider.balance = 0;
    provider.open_plans = 0;
    provider.open_leases = 0;
    provider.rent_recipient = provider_rent_recipient;
    provider.opened_at = now;
    provider.bump = ctx.bumps.provider;
    provider.reserved = [0u8; 32];
    emit!(ProviderOpened {
        provider: provider_key,
        provider_id,
        owner: provider.owner,
        bond,
        uri,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct UpdateProvider<'info> {
    pub signer: Signer<'info>,
    #[account(mut, seeds = [PROVIDER_SEED, provider.owner.as_ref(), provider.provider_id.as_ref()], bump = provider.bump)]
    pub provider: Account<'info, Provider>,
    #[account(seeds = [GRANT_SEED, provider.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
}

pub fn update_provider(ctx: Context<UpdateProvider>, uri: Option<Vec<u8>>) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    crate::auth::require_owner_or_grantee(
        &ctx.accounts.provider.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_UPDATE_PROVIDER,
        &[ctx.accounts.provider.key()],
        now,
    )?;
    let provider_key = ctx.accounts.provider.key();
    let provider: &mut Provider = &mut ctx.accounts.provider;
    if let Some(uri) = uri {
        crate::uri::write_uri(&mut provider.uri, &mut provider.uri_len, &uri)?;
    }
    emit!(ProviderUpdated {
        provider: provider_key,
        uri: provider
            .uri
            .get(..usize::from(provider.uri_len))
            .unwrap_or_default()
            .to_vec(),
    });
    Ok(())
}

#[derive(Accounts)]
pub struct ListProvider<'info> {
    pub signer: Signer<'info>,
    #[account(mut, seeds = [PROVIDER_SEED, provider.owner.as_ref(), provider.provider_id.as_ref()], bump = provider.bump)]
    pub provider: Account<'info, Provider>,
    #[account(seeds = [GRANT_SEED, provider.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
}

pub fn list_provider(ctx: Context<ListProvider>) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    crate::auth::require_owner_or_grantee(
        &ctx.accounts.provider.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_LIST_PROVIDER,
        &[ctx.accounts.provider.key()],
        now,
    )?;
    ctx.accounts.provider.status = ListingStatus::Listed as u8;
    emit!(ProviderListed {
        provider: ctx.accounts.provider.key(),
    });
    Ok(())
}

#[derive(Accounts)]
pub struct DelistProvider<'info> {
    pub signer: Signer<'info>,
    #[account(mut, seeds = [PROVIDER_SEED, provider.owner.as_ref(), provider.provider_id.as_ref()], bump = provider.bump)]
    pub provider: Account<'info, Provider>,
    #[account(seeds = [GRANT_SEED, provider.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
}

pub fn delist_provider(ctx: Context<DelistProvider>) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    crate::auth::require_owner_or_grantee(
        &ctx.accounts.provider.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_DELIST_PROVIDER,
        &[ctx.accounts.provider.key()],
        now,
    )?;
    ctx.accounts.provider.status = ListingStatus::Delisted as u8;
    emit!(ProviderDelisted {
        provider: ctx.accounts.provider.key(),
    });
    Ok(())
}

#[derive(Accounts)]
pub struct WithdrawProviderRevenue<'info> {
    pub signer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(
        mut,
        seeds = [PROVIDER_SEED, provider.owner.as_ref(), provider.provider_id.as_ref()],
        bump = provider.bump,
        has_one = owner @ DvpnError::Unauthorized
    )]
    pub provider: Account<'info, Provider>,
    #[doc = "CHECK: the provider owner; the grant gate validates it against `signer`."]
    #[account(address = provider.owner)]
    pub owner: UncheckedAccount<'info>,
    #[account(seeds = [GRANT_SEED, provider.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
    #[account(mut, token::mint = config.mint, token::authority = owner)]
    pub owner_token: InterfaceAccount<'info, TokenAccount>,
    #[account(mut, address = config.vault)]
    pub vault: InterfaceAccount<'info, TokenAccount>,
    #[account(address = config.mint)]
    pub mint: InterfaceAccount<'info, Mint>,
    #[account(address = token_2022::ID)]
    pub token_program: Interface<'info, TokenInterface>,
}

pub fn withdraw_provider_revenue(ctx: Context<WithdrawProviderRevenue>, amount: u64) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    crate::auth::require_owner_or_grantee(
        &ctx.accounts.provider.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_WITHDRAW_PROVIDER_REVENUE,
        &[ctx.accounts.provider.key()],
        now,
    )?;
    require!(amount > 0, DvpnError::ParamOutOfRange);
    require!(
        amount <= ctx.accounts.provider.revenue,
        DvpnError::InsufficientRevenue
    );
    crate::transfer::transfer_from_vault(
        &ctx.accounts.token_program,
        &ctx.accounts.vault,
        &ctx.accounts.mint,
        &ctx.accounts.owner_token,
        &ctx.accounts.config.to_account_info(),
        ctx.accounts.config.bump,
        amount,
    )?;
    ctx.accounts.provider.revenue = ctx
        .accounts
        .provider
        .revenue
        .checked_sub(amount)
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
    emit!(ProviderRevenueWithdrawn {
        provider: ctx.accounts.provider.key(),
        destination: ctx.accounts.owner_token.key(),
        amount,
        provider_revenue: ctx.accounts.provider.revenue,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct WithdrawProviderBalance<'info> {
    pub signer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(
        mut,
        seeds = [PROVIDER_SEED, provider.owner.as_ref(), provider.provider_id.as_ref()],
        bump = provider.bump,
        has_one = owner @ DvpnError::Unauthorized
    )]
    pub provider: Account<'info, Provider>,
    #[doc = "CHECK: the provider owner; the grant gate validates it against `signer`."]
    #[account(address = provider.owner)]
    pub owner: UncheckedAccount<'info>,
    #[account(seeds = [GRANT_SEED, provider.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
    #[account(mut, token::mint = config.mint, token::authority = owner)]
    pub owner_token: InterfaceAccount<'info, TokenAccount>,
    #[account(mut, address = config.vault)]
    pub vault: InterfaceAccount<'info, TokenAccount>,
    #[account(address = config.mint)]
    pub mint: InterfaceAccount<'info, Mint>,
    #[account(address = token_2022::ID)]
    pub token_program: Interface<'info, TokenInterface>,
}

pub fn withdraw_provider_balance(ctx: Context<WithdrawProviderBalance>) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    crate::auth::require_owner_or_grantee(
        &ctx.accounts.provider.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_WITHDRAW_PROVIDER_BALANCE,
        &[ctx.accounts.provider.key()],
        now,
    )?;
    let amount = ctx.accounts.provider.balance;
    require!(amount > 0, DvpnError::BalanceZero);
    crate::transfer::transfer_from_vault(
        &ctx.accounts.token_program,
        &ctx.accounts.vault,
        &ctx.accounts.mint,
        &ctx.accounts.owner_token,
        &ctx.accounts.config.to_account_info(),
        ctx.accounts.config.bump,
        amount,
    )?;
    ctx.accounts.provider.balance = 0;
    emit!(ProviderBalanceWithdrawn {
        provider: ctx.accounts.provider.key(),
        destination: ctx.accounts.owner_token.key(),
        amount,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct SweepProviderFees<'info> {
    pub signer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(mut, address = config.vault)]
    pub vault: InterfaceAccount<'info, TokenAccount>,
    #[account(mut, address = config.treasury)]
    pub treasury: InterfaceAccount<'info, TokenAccount>,
    #[account(address = config.mint)]
    pub mint: InterfaceAccount<'info, Mint>,
    #[account(address = token_2022::ID)]
    pub token_program: Interface<'info, TokenInterface>,
}

pub fn sweep_provider_fees(ctx: Context<SweepProviderFees>) -> Result<()> {
    let remaining = ctx.remaining_accounts;
    crate::batch::require_sorted_unique(remaining)?;
    crate::batch::require_same_nonempty_len(remaining.len(), remaining.len())?;
    let mut total_fees: u64 = 0;
    for info in remaining {
        let mut provider: Account<Provider> = Account::try_from(info)?;
        if provider.fees_accrued > 0 {
            total_fees = total_fees
                .checked_add(provider.fees_accrued)
                .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
            emit!(ProviderFeesSwept {
                provider: provider.key(),
                treasury: ctx.accounts.treasury.key(),
                amount: provider.fees_accrued,
            });
            provider.fees_accrued = 0;
            provider.exit(&crate::ID)?;
        }
    }
    require!(total_fees > 0, DvpnError::FeesZero);
    crate::transfer::transfer_from_vault(
        &ctx.accounts.token_program,
        &ctx.accounts.vault,
        &ctx.accounts.mint,
        &ctx.accounts.treasury,
        &ctx.accounts.config.to_account_info(),
        ctx.accounts.config.bump,
        total_fees,
    )?;
    Ok(())
}

#[derive(Accounts)]
pub struct CloseProvider<'info> {
    pub signer: Signer<'info>,
    #[account(mut)]
    pub rent_payer: Signer<'info>,
    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,
    #[account(
        mut,
        close = rent_recipient,
        seeds = [PROVIDER_SEED, provider.owner.as_ref(), provider.provider_id.as_ref()],
        bump = provider.bump,
        has_one = owner @ DvpnError::Unauthorized
    )]
    pub provider: Box<Account<'info, Provider>>,
    #[doc = "CHECK: the provider owner; the grant gate validates it against `signer`."]
    #[account(address = provider.owner)]
    pub owner: UncheckedAccount<'info>,
    #[account(seeds = [GRANT_SEED, provider.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
    #[doc = "CHECK: receives the Provider's rent; pinned to the stored rent recipient."]
    #[account(mut, address = provider.rent_recipient)]
    pub rent_recipient: UncheckedAccount<'info>,
    #[account(
        init,
        payer = rent_payer,
        space = 8 + ProviderUnbonding::INIT_SPACE,
        seeds = [PROVIDER_UNBONDING_SEED, provider.owner.as_ref(), provider.provider_id.as_ref()],
        bump
    )]
    pub provider_unbonding: Account<'info, ProviderUnbonding>,
    #[account(mut, token::mint = config.mint, token::authority = owner)]
    pub owner_token: Box<InterfaceAccount<'info, TokenAccount>>,
    #[account(mut, address = config.vault)]
    pub vault: Box<InterfaceAccount<'info, TokenAccount>>,
    #[account(address = config.mint)]
    pub mint: Box<InterfaceAccount<'info, Mint>>,
    #[account(address = token_2022::ID)]
    pub token_program: Interface<'info, TokenInterface>,
    pub system_program: Program<'info, System>,
}

pub fn close_provider(
    ctx: Context<CloseProvider>,
    provider_unbonding_rent_recipient: Pubkey,
) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    crate::auth::require_owner_or_grantee(
        &ctx.accounts.provider.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_CLOSE_PROVIDER,
        &[ctx.accounts.provider.key()],
        now,
    )?;
    crate::rent::require_rent_recipient(
        &provider_unbonding_rent_recipient,
        &ctx.accounts.signer.key(),
        &ctx.accounts.rent_payer.key(),
    )?;
    require!(
        ctx.accounts.provider.open_plans == 0,
        DvpnError::OpenPlansRemain
    );
    require!(
        ctx.accounts.provider.open_leases == 0,
        DvpnError::OpenLeasesRemain
    );
    let revenue_paid = ctx.accounts.provider.revenue;
    let balance_paid = ctx.accounts.provider.balance;
    let payout = revenue_paid
        .checked_add(balance_paid)
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
    if payout > 0 {
        crate::transfer::transfer_from_vault(
            &ctx.accounts.token_program,
            &ctx.accounts.vault,
            &ctx.accounts.mint,
            &ctx.accounts.owner_token,
            &ctx.accounts.config.to_account_info(),
            ctx.accounts.config.bump,
            payout,
        )?;
        ctx.accounts.provider.revenue = 0;
        ctx.accounts.provider.balance = 0;
    }
    let fees_accrued = ctx.accounts.provider.fees_accrued;
    ctx.accounts.config.fees_accrued = ctx
        .accounts
        .config
        .fees_accrued
        .checked_add(fees_accrued)
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
    ctx.accounts.provider.fees_accrued = 0;

    let provider_unbonding = &mut ctx.accounts.provider_unbonding;
    provider_unbonding.provider_id = ctx.accounts.provider.provider_id;
    provider_unbonding.owner = ctx.accounts.provider.owner;
    provider_unbonding.bond = ctx.accounts.provider.bond;
    provider_unbonding.closed_at = now;
    provider_unbonding.rent_recipient = provider_unbonding_rent_recipient;
    provider_unbonding.bump = ctx.bumps.provider_unbonding;
    provider_unbonding.reserved = [0u8; 32];
    emit!(ProviderClosed {
        provider: ctx.accounts.provider.key(),
        provider_id: provider_unbonding.provider_id,
        owner: provider_unbonding.owner,
        bond: provider_unbonding.bond,
        unbonding: provider_unbonding.key(),
        closed_at: now,
        revenue_paid,
        balance_paid,
        fees_accrued,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct WithdrawProviderBond<'info> {
    pub signer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(
        mut,
        close = rent_recipient,
        seeds = [PROVIDER_UNBONDING_SEED, provider_unbonding.owner.as_ref(), provider_unbonding.provider_id.as_ref()],
        bump = provider_unbonding.bump
    )]
    pub provider_unbonding: Account<'info, ProviderUnbonding>,
    #[doc = "CHECK: the record's owner; the grant gate validates it against `signer`."]
    #[account(address = provider_unbonding.owner)]
    pub owner: UncheckedAccount<'info>,
    #[account(seeds = [GRANT_SEED, owner.key().as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
    #[doc = "CHECK: pinned to the stored rent recipient."]
    #[account(mut, address = provider_unbonding.rent_recipient)]
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

pub fn withdraw_provider_bond(ctx: Context<WithdrawProviderBond>) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    let provider_unbonding = &ctx.accounts.provider_unbonding;
    let provider = match ctx.accounts.grant.as_ref() {
        Some(grant) if grant.target != Pubkey::default() => {
            Pubkey::find_program_address(
                &[
                    PROVIDER_SEED,
                    provider_unbonding.owner.as_ref(),
                    provider_unbonding.provider_id.as_ref(),
                ],
                &crate::ID,
            )
            .0
        }
        _ => Pubkey::default(),
    };
    crate::auth::require_owner_or_grantee(
        &provider_unbonding.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_WITHDRAW_PROVIDER_BOND,
        &[provider, provider_unbonding.key()],
        now,
    )?;
    require!(
        crate::deadline::has_elapsed(
            ctx.accounts.provider_unbonding.closed_at,
            ctx.accounts.config.unbonding_seconds,
            now
        )?,
        DvpnError::UnbondingNotElapsed
    );
    let bond = ctx.accounts.provider_unbonding.bond;
    if bond > 0 {
        crate::transfer::transfer_from_vault(
            &ctx.accounts.token_program,
            &ctx.accounts.vault,
            &ctx.accounts.mint,
            &ctx.accounts.owner_token,
            &ctx.accounts.config.to_account_info(),
            ctx.accounts.config.bump,
            bond,
        )?;
    }
    emit!(ProviderBondWithdrawn {
        unbonding: ctx.accounts.provider_unbonding.key(),
        provider_id: ctx.accounts.provider_unbonding.provider_id,
        owner: ctx.accounts.provider_unbonding.owner,
        destination: ctx.accounts.owner_token.key(),
        bond,
    });
    Ok(())
}
