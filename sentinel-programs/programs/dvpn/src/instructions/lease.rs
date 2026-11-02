use anchor_lang::prelude::*;
use anchor_spl::token_interface::{Mint, TokenAccount, TokenInterface};
use dvpn_core::constants::PAUSE_LEASE_OPENS;
use dvpn_core::enums::{ListingStatus, PositionMode, PositionStatus, PositionTerms};
use dvpn_core::permissions::{
    PERM_CLOSE_LEASE, PERM_FUND_LEASES, PERM_OPEN_LEASE, PERM_START_CLOSE_LEASE,
};
use dvpn_core::seeds::{CONFIG_SEED, GRANT_SEED, LEASE_SEED, NODE_SEED, PROVIDER_SEED};
use dvpn_core::voucher::{Voucher, lease_voucher_message};

use crate::error::DvpnError;
use crate::state::{Config, Grant, Lease, Node, Provider};
use crate::{auth, batch, deadline, events, fee, stream, transfer, verify};

#[derive(Accounts)]
#[instruction(lease_id: [u8; 32])]
pub struct OpenLease<'info> {
    #[account(mut)]
    pub signer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(mut, seeds = [PROVIDER_SEED, provider.owner.as_ref(), provider.provider_id.as_ref()], bump = provider.bump)]
    pub provider: Box<Account<'info, Provider>>,
    #[account(mut, seeds = [GRANT_SEED, provider.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
    #[account(seeds = [NODE_SEED, node.owner.as_ref(), node.node_id.as_ref()], bump = node.bump)]
    pub node: Box<Account<'info, Node>>,
    #[account(init, payer = rent_payer, space = 8 + Lease::INIT_SPACE, seeds = [LEASE_SEED, provider.key().as_ref(), lease_id.as_ref()], bump)]
    pub lease: Account<'info, Lease>,
    #[account(mut, address = config.vault)]
    pub vault: InterfaceAccount<'info, TokenAccount>,
    #[account(address = config.mint)]
    pub mint: InterfaceAccount<'info, Mint>,
    #[account(mut, token::mint = config.mint, token::authority = provider.owner)]
    pub owner_token: InterfaceAccount<'info, TokenAccount>,
    #[account(address = anchor_spl::token_2022::spl_token_2022::ID)]
    pub token_program: Interface<'info, TokenInterface>,
    #[account(mut)]
    pub rent_payer: Signer<'info>,
    pub system_program: Program<'info, System>,
}

pub fn open_lease(
    ctx: Context<OpenLease>,
    lease_id: [u8; 32],
    escrow: u64,
    terms: PositionTerms,
    lease_rent_recipient: Pubkey,
) -> Result<()> {
    let config = &ctx.accounts.config;
    require!(
        config.paused_mask & PAUSE_LEASE_OPENS == 0,
        DvpnError::OperationPaused
    );
    require!(
        ctx.accounts.provider.status == ListingStatus::Listed as u8,
        DvpnError::ProviderNotListed
    );
    let node = &ctx.accounts.node;
    require!(
        node.status == ListingStatus::Listed as u8,
        DvpnError::NodeNotListed
    );
    let now = Clock::get()?.unix_timestamp;
    require!(
        deadline::is_live(node.listed_at, config.node_listing_ttl_seconds, now)?,
        DvpnError::NodeNotLive
    );

    let provider_owner = ctx.accounts.provider.owner;
    let signer = ctx.accounts.signer.key();
    let lease_key = ctx.accounts.lease.key();
    let node_key = ctx.accounts.node.key();
    let provider_key = ctx.accounts.provider.key();
    auth::require_owner_or_grantee(
        &provider_owner,
        &signer,
        ctx.accounts.grant.as_ref(),
        PERM_OPEN_LEASE,
        &[provider_key, node_key, lease_key],
        now,
    )?;
    crate::rent::require_rent_recipient(
        &lease_rent_recipient,
        &signer,
        &ctx.accounts.rent_payer.key(),
    )?;
    let is_delegated = ctx.accounts.grant.is_some();

    let (mode, voucher_signer, rate_per_hour, stream_started_at) = match terms {
        PositionTerms::Voucher { voucher_signer } => {
            verify::require_voucher_signer_prime_order(&voucher_signer)?;
            (PositionMode::Voucher, voucher_signer, 0u64, 0i64)
        }
        PositionTerms::Stream {
            expected_rate_per_hour,
        } => {
            require!(
                expected_rate_per_hour == node.lease_price_per_hour,
                DvpnError::NodePriceChanged
            );
            (
                PositionMode::Stream,
                Pubkey::default(),
                expected_rate_per_hour,
                now,
            )
        }
    };
    if let Some(grant) = ctx.accounts.grant.as_mut() {
        auth::charge_allowance(&mut grant.allowance, escrow)?;
    }

    let provider = &mut ctx.accounts.provider;
    let mut pools = [provider.balance, provider.revenue];
    let shortfall = transfer::draw_from_pools(escrow, &mut pools);
    let [after_balance, after_revenue] = pools;
    let from_balance = provider.balance.saturating_sub(after_balance);
    let from_revenue = provider.revenue.saturating_sub(after_revenue);
    provider.balance = after_balance;
    provider.revenue = after_revenue;
    if shortfall > 0 {
        if is_delegated {
            transfer::transfer_to_vault_as_delegate(
                &ctx.accounts.token_program,
                &ctx.accounts.owner_token,
                &ctx.accounts.mint,
                &ctx.accounts.vault,
                &ctx.accounts.config.to_account_info(),
                ctx.accounts.config.bump,
                shortfall,
            )?;
        } else {
            transfer::transfer_to_vault_as_owner(
                &ctx.accounts.token_program,
                &ctx.accounts.owner_token,
                &ctx.accounts.mint,
                &ctx.accounts.vault,
                &ctx.accounts.signer.to_account_info(),
                shortfall,
            )?;
        }
    }

    let lease = &mut ctx.accounts.lease;
    lease.lease_id = lease_id;
    lease.provider = provider.key();
    lease.node = node_key;
    lease.node_opened_at = ctx.accounts.node.opened_at;
    lease.owner = provider_owner;
    lease.opener = signer;
    lease.mode = mode as u8;
    lease.voucher_signer = voucher_signer;
    lease.rate_per_hour = rate_per_hour;
    lease.stream_base = 0;
    lease.stream_started_at = stream_started_at;
    lease.escrow = escrow;
    lease.redeemed = 0;
    lease.opened_at = now;
    lease.close_started_at = 0;
    lease.status = PositionStatus::Open as u8;
    lease.rent_recipient = lease_rent_recipient;
    lease.bump = ctx.bumps.lease;
    lease.reserved = [0u8; 32];
    provider.open_leases = provider
        .open_leases
        .checked_add(1)
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;

    let allowance =
        auth::allowance_or_unlimited(ctx.accounts.grant.as_ref().map(|grant| grant.allowance));
    emit!(events::LeaseOpened {
        lease: lease.key(),
        lease_id,
        provider: lease.provider,
        node: node_key,
        owner: provider_owner,
        opener: signer,
        mode: lease.mode,
        voucher_signer,
        rate_per_hour,
        escrow,
        from_balance,
        from_revenue,
        provider_balance: provider.balance,
        provider_revenue: provider.revenue,
        allowance,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct FundLeases<'info> {
    #[account(mut)]
    pub signer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(mut, seeds = [PROVIDER_SEED, provider.owner.as_ref(), provider.provider_id.as_ref()], bump = provider.bump)]
    pub provider: Account<'info, Provider>,
    #[account(mut, seeds = [GRANT_SEED, provider.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
    #[account(mut, address = config.vault)]
    pub vault: InterfaceAccount<'info, TokenAccount>,
    #[account(address = config.mint)]
    pub mint: InterfaceAccount<'info, Mint>,
    #[account(mut, token::mint = config.mint, token::authority = provider.owner)]
    pub owner_token: InterfaceAccount<'info, TokenAccount>,
    #[account(address = anchor_spl::token_2022::spl_token_2022::ID)]
    pub token_program: Interface<'info, TokenInterface>,
}

pub fn fund_leases(ctx: Context<FundLeases>, amounts: Vec<u64>) -> Result<()> {
    let provider_owner = ctx.accounts.provider.owner;
    let signer = ctx.accounts.signer.key();
    let now = Clock::get()?.unix_timestamp;
    let provider_key = ctx.accounts.provider.key();
    let is_delegated = ctx.accounts.grant.is_some();
    let grant_target = ctx
        .accounts
        .grant
        .as_ref()
        .map_or(Pubkey::default(), |grant| grant.target);
    auth::require_owner_or_grantee(
        &provider_owner,
        &signer,
        ctx.accounts.grant.as_ref(),
        PERM_FUND_LEASES,
        &[grant_target],
        now,
    )?;
    let total = amounts
        .iter()
        .try_fold(0u64, |total, amount| total.checked_add(*amount))
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
    if let Some(grant) = ctx.accounts.grant.as_mut() {
        auth::charge_allowance(&mut grant.allowance, total)?;
    }

    let remaining = ctx.remaining_accounts;
    batch::require_sorted_unique(remaining)?;
    batch::require_same_nonempty_len(remaining.len(), amounts.len())?;

    let mut pools = [ctx.accounts.provider.balance, ctx.accounts.provider.revenue];
    let mut wallet_shortfall = 0u64;
    for (idx, info) in remaining.iter().enumerate() {
        let mut lease = Account::<Lease>::try_from(info)?;
        require_keys_eq!(
            lease.provider,
            ctx.accounts.provider.key(),
            ErrorCode::ConstraintHasOne
        );
        require!(
            lease.status == PositionStatus::Open as u8,
            DvpnError::LeaseNotOpen
        );
        require!(
            auth::is_covered_by_grant(&grant_target, &[provider_key, lease.key(), lease.node]),
            DvpnError::GrantTargetMismatch
        );
        let amount = *amounts
            .get(idx)
            .ok_or_else(|| error!(DvpnError::BatchLengthMismatch))?;
        if lease.mode == PositionMode::Stream as u8
            && let Some((base, started)) = stream::checkpoint_if_exhausted(
                lease.escrow,
                lease.stream_base,
                lease.rate_per_hour,
                lease.stream_started_at,
                now,
            )
        {
            lease.stream_base = base;
            lease.stream_started_at = started;
        }
        let [balance_before, revenue_before] = pools;
        let shortfall = transfer::draw_from_pools(amount, &mut pools);
        let [balance_after, revenue_after] = pools;
        let (from_balance, from_revenue) = (
            balance_before.saturating_sub(balance_after),
            revenue_before.saturating_sub(revenue_after),
        );
        wallet_shortfall = wallet_shortfall
            .checked_add(shortfall)
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        lease.escrow = lease
            .escrow
            .checked_add(amount)
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        emit!(events::LeaseFunded {
            lease: lease.key(),
            funder: signer,
            amount,
            escrow: lease.escrow,
            stream_base: lease.stream_base,
            stream_started_at: lease.stream_started_at,
            from_balance,
            from_revenue,
            provider_balance: balance_after,
            provider_revenue: revenue_after,
            allowance: auth::allowance_or_unlimited(
                ctx.accounts.grant.as_ref().map(|grant| grant.allowance)
            )
        });
        lease.exit(&crate::ID)?;
    }
    let [final_balance, final_revenue] = pools;
    ctx.accounts.provider.balance = final_balance;
    ctx.accounts.provider.revenue = final_revenue;
    if wallet_shortfall > 0 {
        if is_delegated {
            transfer::transfer_to_vault_as_delegate(
                &ctx.accounts.token_program,
                &ctx.accounts.owner_token,
                &ctx.accounts.mint,
                &ctx.accounts.vault,
                &ctx.accounts.config.to_account_info(),
                ctx.accounts.config.bump,
                wallet_shortfall,
            )?;
        } else {
            transfer::transfer_to_vault_as_owner(
                &ctx.accounts.token_program,
                &ctx.accounts.owner_token,
                &ctx.accounts.mint,
                &ctx.accounts.vault,
                &ctx.accounts.signer.to_account_info(),
                wallet_shortfall,
            )?;
        }
    }
    Ok(())
}

#[derive(Accounts)]
pub struct RedeemLeaseVouchers<'info> {
    pub signer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(mut, seeds = [NODE_SEED, node.owner.as_ref(), node.node_id.as_ref()], bump = node.bump)]
    pub node: Account<'info, Node>,
}

pub fn redeem_lease_vouchers(
    ctx: Context<RedeemLeaseVouchers>,
    vouchers: Vec<Voucher>,
) -> Result<()> {
    let fee_bps = ctx.accounts.config.fee_bps;
    let remaining = ctx.remaining_accounts;
    batch::require_sorted_unique(remaining)?;
    batch::require_same_nonempty_len(remaining.len(), vouchers.len())?;
    let node = &mut ctx.accounts.node;
    let node_key = node.key();
    let mut redeemed_any = false;
    for (idx, info) in remaining.iter().enumerate() {
        let mut lease = Account::<Lease>::try_from(info)?;
        require_keys_eq!(lease.node, node_key, ErrorCode::ConstraintHasOne);
        require!(
            lease.node_opened_at == node.opened_at,
            DvpnError::NodeReopened
        );
        require!(
            lease.mode == PositionMode::Voucher as u8,
            DvpnError::ModeNotVoucher
        );
        let voucher = vouchers
            .get(idx)
            .ok_or_else(|| error!(DvpnError::BatchLengthMismatch))?;
        let msg = lease_voucher_message(&lease.key(), lease.opened_at, voucher.cumulative_amount);
        verify::require_voucher_signature(&lease.voucher_signer, &msg, &voucher.signature)?;
        require!(
            voucher.cumulative_amount <= lease.escrow,
            DvpnError::VoucherExceedsEscrow
        );
        if voucher.cumulative_amount <= lease.redeemed {
            continue;
        }
        let gross_amount = voucher
            .cumulative_amount
            .checked_sub(lease.redeemed)
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        let fee = fee::fee_amount(gross_amount, fee_bps)?;
        node.revenue = node
            .revenue
            .checked_add(
                gross_amount
                    .checked_sub(fee)
                    .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?,
            )
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        node.fees_accrued = node
            .fees_accrued
            .checked_add(fee)
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        lease.redeemed = voucher.cumulative_amount;
        redeemed_any = true;
        emit!(events::LeaseVoucherRedeemed {
            lease: lease.key(),
            node: node_key,
            gross_amount,
            fee,
            redeemed_total: lease.redeemed,
            node_revenue: node.revenue,
            node_fees_accrued: node.fees_accrued,
        });
        lease.exit(&crate::ID)?;
    }
    require!(redeemed_any, DvpnError::RedeemableZero);
    Ok(())
}

#[derive(Accounts)]
pub struct RedeemLeaseStreams<'info> {
    pub signer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(mut, seeds = [NODE_SEED, node.owner.as_ref(), node.node_id.as_ref()], bump = node.bump)]
    pub node: Account<'info, Node>,
}

pub fn redeem_lease_streams(ctx: Context<RedeemLeaseStreams>) -> Result<()> {
    let fee_bps = ctx.accounts.config.fee_bps;
    let now = Clock::get()?.unix_timestamp;
    let remaining = ctx.remaining_accounts;
    batch::require_sorted_unique(remaining)?;
    batch::require_same_nonempty_len(remaining.len(), remaining.len())?;
    let node = &mut ctx.accounts.node;
    let node_key = node.key();
    let mut redeemed_any = false;
    for info in remaining.iter() {
        let mut lease = Account::<Lease>::try_from(info)?;
        require_keys_eq!(lease.node, node_key, ErrorCode::ConstraintHasOne);
        require!(
            lease.node_opened_at == node.opened_at,
            DvpnError::NodeReopened
        );
        require!(
            lease.mode == PositionMode::Stream as u8,
            DvpnError::ModeNotStream
        );
        let accrued = stream::accrued(
            lease.escrow,
            lease.stream_base,
            lease.rate_per_hour,
            lease.stream_started_at,
            now,
        );
        if accrued <= lease.redeemed {
            continue;
        }
        let gross_amount = accrued
            .checked_sub(lease.redeemed)
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        let fee = fee::fee_amount(gross_amount, fee_bps)?;
        node.revenue = node
            .revenue
            .checked_add(
                gross_amount
                    .checked_sub(fee)
                    .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?,
            )
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        node.fees_accrued = node
            .fees_accrued
            .checked_add(fee)
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        lease.redeemed = accrued;
        redeemed_any = true;
        emit!(events::LeaseStreamRedeemed {
            lease: lease.key(),
            node: node_key,
            gross_amount,
            fee,
            redeemed_total: lease.redeemed,
            node_revenue: node.revenue,
            node_fees_accrued: node.fees_accrued,
        });
        lease.exit(&crate::ID)?;
    }
    require!(redeemed_any, DvpnError::RedeemableZero);
    Ok(())
}

#[derive(Accounts)]
pub struct StartCloseLease<'info> {
    pub signer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(seeds = [PROVIDER_SEED, provider.owner.as_ref(), provider.provider_id.as_ref()], bump = provider.bump)]
    pub provider: Account<'info, Provider>,
    #[account(mut, has_one = provider @ ErrorCode::ConstraintHasOne)]
    pub lease: Account<'info, Lease>,
    #[account(seeds = [GRANT_SEED, provider.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
}

pub fn start_close_lease(ctx: Context<StartCloseLease>) -> Result<()> {
    let lease = &mut ctx.accounts.lease;
    require!(
        lease.mode == PositionMode::Voucher as u8,
        DvpnError::ModeNotVoucher
    );
    require!(
        lease.status == PositionStatus::Open as u8,
        DvpnError::LeaseNotOpen
    );
    let now = Clock::get()?.unix_timestamp;
    auth::require_owner_or_grantee(
        &lease.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_START_CLOSE_LEASE,
        &[lease.provider, lease.key(), lease.node],
        now,
    )?;
    lease.status = PositionStatus::Closing as u8;
    lease.close_started_at = now;
    emit!(events::LeaseCloseStarted {
        lease: lease.key(),
        close_started_at: lease.close_started_at,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct CloseLease<'info> {
    pub signer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(mut, seeds = [PROVIDER_SEED, provider.owner.as_ref(), provider.provider_id.as_ref()], bump = provider.bump)]
    pub provider: Account<'info, Provider>,
    #[account(mut, close = rent_recipient, has_one = provider @ ErrorCode::ConstraintHasOne)]
    pub lease: Account<'info, Lease>,
    #[account(seeds = [GRANT_SEED, provider.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
    #[doc = "CHECK: the stored node; required only on a Stream close. `address` pins it."]
    #[account(mut, address = lease.node)]
    pub node: Option<UncheckedAccount<'info>>,
    #[doc = "CHECK: pinned to the stored rent recipient."]
    #[account(mut, address = lease.rent_recipient)]
    pub rent_recipient: UncheckedAccount<'info>,
}

pub fn close_lease<'info>(ctx: Context<'info, CloseLease<'info>>) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    let fee_bps = ctx.accounts.config.fee_bps;
    let grace_seconds = ctx.accounts.config.lease_close_grace_seconds;

    let node_info: Option<&AccountInfo> = match &ctx.accounts.node {
        Some(node) => Some(node.as_ref()),
        None => None,
    };
    let lease = &mut ctx.accounts.lease;
    let lease_key = lease.key();
    let forfeited = if lease.mode == PositionMode::Voucher as u8 {
        require!(
            lease.status == PositionStatus::Closing as u8,
            DvpnError::LeaseNotClosing
        );
        require!(
            deadline::has_elapsed(lease.close_started_at, grace_seconds, now)?,
            DvpnError::GraceNotElapsed
        );
        0u64
    } else {
        auth::require_owner_or_grantee(
            &lease.owner,
            &ctx.accounts.signer.key(),
            ctx.accounts.grant.as_ref(),
            PERM_CLOSE_LEASE,
            &[lease.provider, lease_key, lease.node],
            now,
        )?;
        settle_stream_lease(fee_bps, lease, lease_key, now, node_info)?
    };
    let returned = lease
        .escrow
        .checked_sub(lease.redeemed)
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
    let (provider_key, node_key, owner, mode, escrow, redeemed) = (
        lease.provider,
        lease.node,
        lease.owner,
        lease.mode,
        lease.escrow,
        lease.redeemed,
    );
    let provider = &mut ctx.accounts.provider;
    provider.balance = provider
        .balance
        .checked_add(returned)
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
    provider.open_leases = provider
        .open_leases
        .checked_sub(1)
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
    emit!(events::LeaseClosed {
        lease: lease_key,
        provider: provider_key,
        node: node_key,
        owner,
        mode,
        escrow,
        redeemed,
        returned,
        forfeited,
        provider_balance: provider.balance,
    });
    Ok(())
}

fn settle_stream_lease<'info>(
    fee_bps: u16,
    lease: &mut Lease,
    lease_key: Pubkey,
    now: i64,
    node: Option<&'info AccountInfo<'info>>,
) -> Result<u64> {
    let accrued = stream::accrued(
        lease.escrow,
        lease.stream_base,
        lease.rate_per_hour,
        lease.stream_started_at,
        now,
    );
    let node = node.ok_or_else(|| error!(ErrorCode::ConstraintAccountIsNone))?;
    if *node.owner == anchor_lang::system_program::ID && node.data_is_empty() {
        let forfeited = accrued
            .checked_sub(lease.redeemed)
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        return Ok(forfeited);
    }
    let mut node = Account::<Node>::try_from(node)?;
    if node.opened_at != lease.node_opened_at {
        let forfeited = accrued
            .checked_sub(lease.redeemed)
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        return Ok(forfeited);
    }
    if accrued > lease.redeemed {
        let gross_amount = accrued
            .checked_sub(lease.redeemed)
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        let fee = fee::fee_amount(gross_amount, fee_bps)?;
        node.revenue = node
            .revenue
            .checked_add(
                gross_amount
                    .checked_sub(fee)
                    .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?,
            )
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        node.fees_accrued = node
            .fees_accrued
            .checked_add(fee)
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        lease.redeemed = accrued;
        emit!(events::LeaseStreamRedeemed {
            lease: lease_key,
            node: node.key(),
            gross_amount,
            fee,
            redeemed_total: lease.redeemed,
            node_revenue: node.revenue,
            node_fees_accrued: node.fees_accrued,
        });
        node.exit(&crate::ID)?;
    }
    Ok(0)
}
