use anchor_lang::prelude::*;
use anchor_spl::token_interface::{Mint, TokenAccount, TokenInterface};
use dvpn_core::constants::PAUSE_CHANNEL_OPENS;
use dvpn_core::enums::{ListingStatus, PositionMode, PositionStatus, PositionTerms};
use dvpn_core::permissions::{
    PERM_CLOSE_CHANNEL, PERM_FUND_CHANNELS, PERM_OPEN_CHANNEL, PERM_START_CLOSE_CHANNEL,
};
use dvpn_core::seeds::{CHANNEL_SEED, CONFIG_SEED, GRANT_SEED, NODE_SEED, USER_SEED};
use dvpn_core::voucher::{PartialVoucher, Voucher, channel_voucher_message};

use crate::error::DvpnError;
use crate::state::{Channel, Config, Grant, Node, User};
use crate::{auth, batch, deadline, events, fee, stream, transfer, verify};

#[derive(Accounts)]
#[instruction(channel_id: [u8; 32])]
pub struct OpenChannel<'info> {
    #[account(mut)]
    pub signer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,
    #[doc = "CHECK: the wallet the channel belongs to; the grant gate validates it against `signer`."]
    pub owner: UncheckedAccount<'info>,
    #[account(mut, seeds = [GRANT_SEED, owner.key().as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
    #[account(init_if_needed, payer = rent_payer, space = 8 + User::INIT_SPACE, seeds = [USER_SEED, owner.key().as_ref()], bump)]
    pub user: Account<'info, User>,
    #[account(seeds = [NODE_SEED, node.owner.as_ref(), node.node_id.as_ref()], bump = node.bump)]
    pub node: Box<Account<'info, Node>>,
    #[account(init, payer = rent_payer, space = 8 + Channel::INIT_SPACE, seeds = [CHANNEL_SEED, owner.key().as_ref(), channel_id.as_ref()], bump)]
    pub channel: Account<'info, Channel>,
    #[account(mut, address = config.vault)]
    pub vault: InterfaceAccount<'info, TokenAccount>,
    #[account(address = config.mint)]
    pub mint: InterfaceAccount<'info, Mint>,
    #[account(mut, token::mint = config.mint, token::authority = owner)]
    pub owner_token: InterfaceAccount<'info, TokenAccount>,
    #[account(address = anchor_spl::token_2022::spl_token_2022::ID)]
    pub token_program: Interface<'info, TokenInterface>,
    #[account(mut)]
    pub rent_payer: Signer<'info>,
    pub system_program: Program<'info, System>,
}

pub fn open_channel(
    ctx: Context<OpenChannel>,
    channel_id: [u8; 32],
    escrow: u64,
    terms: PositionTerms,
    channel_rent_recipient: Pubkey,
    user_rent_recipient: Pubkey,
) -> Result<()> {
    let config = &ctx.accounts.config;
    require!(
        config.paused_mask & PAUSE_CHANNEL_OPENS == 0,
        DvpnError::OperationPaused
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

    let owner = ctx.accounts.owner.key();
    let signer = ctx.accounts.signer.key();
    auth::require_owner_or_grantee(
        &owner,
        &signer,
        ctx.accounts.grant.as_ref(),
        PERM_OPEN_CHANNEL,
        &[ctx.accounts.node.key(), ctx.accounts.channel.key()],
        now,
    )?;
    crate::rent::require_rent_recipient(
        &channel_rent_recipient,
        &signer,
        &ctx.accounts.rent_payer.key(),
    )?;
    crate::rent::require_rent_recipient(
        &user_rent_recipient,
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
                expected_rate_per_hour == node.channel_price_per_hour,
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

    let user = &mut ctx.accounts.user;
    if user.owner == Pubkey::default() {
        user.owner = owner;
        user.rent_recipient = user_rent_recipient;
        user.bump = ctx.bumps.user;
        user.reserved = [0u8; 32];
        emit!(events::UserCreated {
            user: user.key(),
            owner
        });
    }
    if let Some(grant) = ctx.accounts.grant.as_mut() {
        auth::charge_allowance(&mut grant.allowance, escrow)?;
    }

    let mut pool = [user.balance];
    let shortfall = transfer::draw_from_pools(escrow, &mut pool);
    let [after_balance] = pool;
    user.balance = after_balance;
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
    let from_balance = escrow
        .checked_sub(shortfall)
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;

    let channel = &mut ctx.accounts.channel;
    channel.channel_id = channel_id;
    channel.owner = owner;
    channel.node = node.key();
    channel.node_opened_at = node.opened_at;
    channel.opener = signer;
    channel.mode = mode as u8;
    channel.voucher_signer = voucher_signer;
    channel.rate_per_hour = rate_per_hour;
    channel.stream_base = 0;
    channel.stream_started_at = stream_started_at;
    channel.escrow = escrow;
    channel.redeemed = 0;
    channel.opened_at = now;
    channel.close_started_at = 0;
    channel.status = PositionStatus::Open as u8;
    channel.rent_recipient = channel_rent_recipient;
    channel.bump = ctx.bumps.channel;
    channel.reserved = [0u8; 32];
    user.open_channels = user
        .open_channels
        .checked_add(1)
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;

    let allowance =
        auth::allowance_or_unlimited(ctx.accounts.grant.as_ref().map(|grant| grant.allowance));
    emit!(events::ChannelOpened {
        channel: channel.key(),
        channel_id,
        owner,
        node: channel.node,
        opener: signer,
        mode: channel.mode,
        voucher_signer,
        rate_per_hour,
        escrow,
        from_balance,
        user_balance: user.balance,
        allowance,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct FundChannels<'info> {
    #[account(mut)]
    pub signer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[doc = "CHECK: wallet; validated against `signer` by the grant gate."]
    pub owner: UncheckedAccount<'info>,
    #[account(mut, seeds = [USER_SEED, owner.key().as_ref()], bump = user.bump)]
    pub user: Account<'info, User>,
    #[account(mut, seeds = [GRANT_SEED, owner.key().as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
    #[account(mut, address = config.vault)]
    pub vault: InterfaceAccount<'info, TokenAccount>,
    #[account(address = config.mint)]
    pub mint: InterfaceAccount<'info, Mint>,
    #[account(mut, token::mint = config.mint, token::authority = owner)]
    pub owner_token: InterfaceAccount<'info, TokenAccount>,
    #[account(address = anchor_spl::token_2022::spl_token_2022::ID)]
    pub token_program: Interface<'info, TokenInterface>,
}

pub fn fund_channels(ctx: Context<FundChannels>, amounts: Vec<u64>) -> Result<()> {
    let owner = ctx.accounts.owner.key();
    let signer = ctx.accounts.signer.key();
    let now = Clock::get()?.unix_timestamp;
    let is_delegated = ctx.accounts.grant.is_some();
    let grant_target = ctx
        .accounts
        .grant
        .as_ref()
        .map_or(Pubkey::default(), |grant| grant.target);
    auth::require_owner_or_grantee(
        &owner,
        &signer,
        ctx.accounts.grant.as_ref(),
        PERM_FUND_CHANNELS,
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

    let mut pool = [ctx.accounts.user.balance];
    let mut wallet_shortfall = 0u64;
    for (idx, info) in remaining.iter().enumerate() {
        let mut channel = Account::<Channel>::try_from(info)?;
        require_keys_eq!(channel.owner, owner, ErrorCode::ConstraintHasOne);
        require!(
            channel.status == PositionStatus::Open as u8,
            DvpnError::ChannelNotOpen
        );
        require!(
            auth::is_covered_by_grant(&grant_target, &[channel.key(), channel.node]),
            DvpnError::GrantTargetMismatch
        );
        let amount = *amounts
            .get(idx)
            .ok_or_else(|| error!(DvpnError::BatchLengthMismatch))?;
        if channel.mode == PositionMode::Stream as u8
            && let Some((base, started)) = stream::checkpoint_if_exhausted(
                channel.escrow,
                channel.stream_base,
                channel.rate_per_hour,
                channel.stream_started_at,
                now,
            )
        {
            channel.stream_base = base;
            channel.stream_started_at = started;
        }
        let [before] = pool;
        let shortfall = transfer::draw_from_pools(amount, &mut pool);
        let [after] = pool;
        let from_balance = before.saturating_sub(after);
        wallet_shortfall = wallet_shortfall
            .checked_add(shortfall)
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        channel.escrow = channel
            .escrow
            .checked_add(amount)
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        emit!(events::ChannelFunded {
            channel: channel.key(),
            funder: signer,
            amount,
            escrow: channel.escrow,
            stream_base: channel.stream_base,
            stream_started_at: channel.stream_started_at,
            from_balance,
            user_balance: after,
            allowance: auth::allowance_or_unlimited(
                ctx.accounts.grant.as_ref().map(|grant| grant.allowance)
            )
        });
        channel.exit(&crate::ID)?;
    }
    let [final_balance] = pool;
    ctx.accounts.user.balance = final_balance;
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
pub struct RedeemChannelVouchers<'info> {
    pub signer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(mut, seeds = [NODE_SEED, node.owner.as_ref(), node.node_id.as_ref()], bump = node.bump)]
    pub node: Account<'info, Node>,
}

pub fn redeem_channel_vouchers(
    ctx: Context<RedeemChannelVouchers>,
    vouchers: Vec<Voucher>,
) -> Result<()> {
    redeem_channel_claims(ctx, vouchers.into_iter().map(PartialVoucher::from).collect())
}

/// Like `redeem_channel_vouchers`, but each voucher claims only `amount` of its signed total.
pub fn redeem_channel_vouchers_partial(
    ctx: Context<RedeemChannelVouchers>,
    vouchers: Vec<PartialVoucher>,
) -> Result<()> {
    redeem_channel_claims(ctx, vouchers)
}

fn redeem_channel_claims(
    ctx: Context<RedeemChannelVouchers>,
    vouchers: Vec<PartialVoucher>,
) -> Result<()> {
    let fee_bps = ctx.accounts.config.fee_bps;
    let remaining = ctx.remaining_accounts;
    batch::require_sorted_unique(remaining)?;
    batch::require_same_nonempty_len(remaining.len(), vouchers.len())?;
    let node = &mut ctx.accounts.node;
    let node_key = node.key();
    let mut redeemed_any = false;
    for (idx, info) in remaining.iter().enumerate() {
        let mut channel = Account::<Channel>::try_from(info)?;
        require_keys_eq!(channel.node, node_key, ErrorCode::ConstraintHasOne);
        require!(
            channel.node_opened_at == node.opened_at,
            DvpnError::NodeReopened
        );
        require!(
            channel.mode == PositionMode::Voucher as u8,
            DvpnError::ModeNotVoucher
        );
        let voucher = vouchers
            .get(idx)
            .ok_or_else(|| error!(DvpnError::BatchLengthMismatch))?;
        let msg =
            channel_voucher_message(&channel.key(), channel.opened_at, voucher.cumulative_amount);
        verify::require_voucher_signature(&channel.voucher_signer, &msg, &voucher.signature)?;
        require!(
            voucher.cumulative_amount <= channel.escrow,
            DvpnError::VoucherExceedsEscrow
        );
        require!(
            voucher.amount <= voucher.cumulative_amount,
            DvpnError::VoucherClaimExceedsSigned
        );
        if voucher.amount <= channel.redeemed {
            continue;
        }
        let gross_amount = voucher
            .amount
            .checked_sub(channel.redeemed)
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
        channel.redeemed = voucher.amount;
        redeemed_any = true;
        emit!(events::ChannelVoucherRedeemed {
            channel: channel.key(),
            node: node_key,
            gross_amount,
            fee,
            redeemed_total: channel.redeemed,
            node_revenue: node.revenue,
            node_fees_accrued: node.fees_accrued,
        });
        channel.exit(&crate::ID)?;
    }
    require!(redeemed_any, DvpnError::RedeemableZero);
    Ok(())
}

#[derive(Accounts)]
pub struct RedeemChannelStreams<'info> {
    pub signer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(mut, seeds = [NODE_SEED, node.owner.as_ref(), node.node_id.as_ref()], bump = node.bump)]
    pub node: Account<'info, Node>,
}

pub fn redeem_channel_streams(ctx: Context<RedeemChannelStreams>) -> Result<()> {
    let fee_bps = ctx.accounts.config.fee_bps;
    let now = Clock::get()?.unix_timestamp;
    let remaining = ctx.remaining_accounts;
    batch::require_sorted_unique(remaining)?;
    batch::require_same_nonempty_len(remaining.len(), remaining.len())?;
    let node = &mut ctx.accounts.node;
    let node_key = node.key();
    let mut redeemed_any = false;
    for info in remaining.iter() {
        let mut channel = Account::<Channel>::try_from(info)?;
        require_keys_eq!(channel.node, node_key, ErrorCode::ConstraintHasOne);
        require!(
            channel.node_opened_at == node.opened_at,
            DvpnError::NodeReopened
        );
        require!(
            channel.mode == PositionMode::Stream as u8,
            DvpnError::ModeNotStream
        );
        let accrued = stream::accrued(
            channel.escrow,
            channel.stream_base,
            channel.rate_per_hour,
            channel.stream_started_at,
            now,
        );
        if accrued <= channel.redeemed {
            continue;
        }
        let gross_amount = accrued
            .checked_sub(channel.redeemed)
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
        channel.redeemed = accrued;
        redeemed_any = true;
        emit!(events::ChannelStreamRedeemed {
            channel: channel.key(),
            node: node_key,
            gross_amount,
            fee,
            redeemed_total: channel.redeemed,
            node_revenue: node.revenue,
            node_fees_accrued: node.fees_accrued,
        });
        channel.exit(&crate::ID)?;
    }
    require!(redeemed_any, DvpnError::RedeemableZero);
    Ok(())
}

#[derive(Accounts)]
pub struct StartCloseChannel<'info> {
    pub signer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(mut)]
    pub channel: Account<'info, Channel>,
    #[account(seeds = [GRANT_SEED, channel.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
}

pub fn start_close_channel(ctx: Context<StartCloseChannel>) -> Result<()> {
    let channel = &mut ctx.accounts.channel;
    require!(
        channel.mode == PositionMode::Voucher as u8,
        DvpnError::ModeNotVoucher
    );
    require!(
        channel.status == PositionStatus::Open as u8,
        DvpnError::ChannelNotOpen
    );
    let now = Clock::get()?.unix_timestamp;
    auth::require_owner_or_grantee(
        &channel.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_START_CLOSE_CHANNEL,
        &[channel.key(), channel.node],
        now,
    )?;
    channel.status = PositionStatus::Closing as u8;
    channel.close_started_at = now;
    emit!(events::ChannelCloseStarted {
        channel: channel.key(),
        close_started_at: channel.close_started_at,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct CloseChannel<'info> {
    pub signer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(mut, close = rent_recipient)]
    pub channel: Account<'info, Channel>,
    #[account(mut, seeds = [USER_SEED, channel.owner.as_ref()], bump = user.bump)]
    pub user: Account<'info, User>,
    #[account(seeds = [GRANT_SEED, channel.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
    #[doc = "CHECK: the stored node; required only on a Stream close. `address` pins it."]
    #[account(mut, address = channel.node)]
    pub node: Option<UncheckedAccount<'info>>,
    #[doc = "CHECK: pinned to the stored rent recipient."]
    #[account(mut, address = channel.rent_recipient)]
    pub rent_recipient: UncheckedAccount<'info>,
}

pub fn close_channel<'info>(ctx: Context<'info, CloseChannel<'info>>) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    let fee_bps = ctx.accounts.config.fee_bps;
    let grace_seconds = ctx.accounts.config.channel_close_grace_seconds;

    let node_info: Option<&AccountInfo> = match &ctx.accounts.node {
        Some(node) => Some(node.as_ref()),
        None => None,
    };
    let channel_key = ctx.accounts.channel.key();
    let channel = &mut ctx.accounts.channel;
    let forfeited = if channel.mode == PositionMode::Voucher as u8 {
        require!(
            channel.status == PositionStatus::Closing as u8,
            DvpnError::ChannelNotClosing
        );
        require!(
            deadline::has_elapsed(channel.close_started_at, grace_seconds, now)?,
            DvpnError::GraceNotElapsed
        );
        0u64
    } else {
        auth::require_owner_or_grantee(
            &channel.owner,
            &ctx.accounts.signer.key(),
            ctx.accounts.grant.as_ref(),
            PERM_CLOSE_CHANNEL,
            &[channel_key, channel.node],
            now,
        )?;
        settle_stream_channel(fee_bps, channel, channel_key, now, node_info)?
    };
    let returned = channel
        .escrow
        .checked_sub(channel.redeemed)
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
    let mode = channel.mode;
    let (owner, node_key) = (channel.owner, channel.node);
    let (escrow, redeemed) = (channel.escrow, channel.redeemed);
    let user = &mut ctx.accounts.user;
    user.balance = user
        .balance
        .checked_add(returned)
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
    user.open_channels = user
        .open_channels
        .checked_sub(1)
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
    emit!(events::ChannelClosed {
        channel: channel_key,
        owner,
        node: node_key,
        mode,
        escrow,
        redeemed,
        returned,
        forfeited,
        user_balance: user.balance,
    });
    Ok(())
}

fn settle_stream_channel<'info>(
    fee_bps: u16,
    channel: &mut Channel,
    channel_key: Pubkey,
    now: i64,
    node: Option<&'info AccountInfo<'info>>,
) -> Result<u64> {
    let accrued = stream::accrued(
        channel.escrow,
        channel.stream_base,
        channel.rate_per_hour,
        channel.stream_started_at,
        now,
    );
    let node = node.ok_or_else(|| error!(ErrorCode::ConstraintAccountIsNone))?;
    if *node.owner == anchor_lang::system_program::ID && node.data_is_empty() {
        let forfeited = accrued
            .checked_sub(channel.redeemed)
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        return Ok(forfeited);
    }
    let mut node = Account::<Node>::try_from(node)?;
    if node.opened_at != channel.node_opened_at {
        let forfeited = accrued
            .checked_sub(channel.redeemed)
            .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
        return Ok(forfeited);
    }
    if accrued > channel.redeemed {
        let gross_amount = accrued
            .checked_sub(channel.redeemed)
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
        channel.redeemed = accrued;
        emit!(events::ChannelStreamRedeemed {
            channel: channel_key,
            node: node.key(),
            gross_amount,
            fee,
            redeemed_total: channel.redeemed,
            node_revenue: node.revenue,
            node_fees_accrued: node.fees_accrued,
        });
        node.exit(&crate::ID)?;
    }
    Ok(0)
}
