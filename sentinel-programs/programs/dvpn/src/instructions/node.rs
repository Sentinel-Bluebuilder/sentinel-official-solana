use anchor_lang::prelude::*;
use anchor_spl::token_2022;
use anchor_spl::token_interface::{Mint, TokenAccount, TokenInterface};
use dvpn_core::args::NodePrices;
use dvpn_core::constants::PAUSE_NODE_OPENS;
use dvpn_core::enums::ListingStatus;
use dvpn_core::permissions::{
    PERM_CLOSE_NODE, PERM_DELIST_NODE, PERM_LIST_NODE, PERM_OPEN_NODE, PERM_UPDATE_NODE,
    PERM_WITHDRAW_NODE_BOND, PERM_WITHDRAW_NODE_REVENUE,
};
use dvpn_core::seeds::{CONFIG_SEED, GRANT_SEED, NODE_SEED, NODE_UNBONDING_SEED};

use crate::error::DvpnError;
use crate::events::{
    NodeBondWithdrawn, NodeClosed, NodeDelisted, NodeFeesSwept, NodeListed, NodeOpened,
    NodeRevenueWithdrawn, NodeUpdated,
};
use crate::state::{Config, Grant, Node, NodeUnbonding};

#[derive(Accounts)]
#[instruction(node_id: [u8; 32])]
pub struct OpenNode<'info> {
    pub signer: Signer<'info>,
    #[account(mut)]
    pub rent_payer: Signer<'info>,
    #[doc = "CHECK: the wallet the node belongs to; the grant gate validates it against `signer`."]
    pub owner: UncheckedAccount<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(mut, seeds = [GRANT_SEED, owner.key().as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
    #[account(
        init,
        payer = rent_payer,
        space = 8 + Node::INIT_SPACE,
        seeds = [NODE_SEED, owner.key().as_ref(), node_id.as_ref()],
        bump
    )]
    pub node: Account<'info, Node>,
    #[doc = "CHECK: parked-bond address; must be System-owned with no data (BondPending otherwise)."]
    #[account(seeds = [NODE_UNBONDING_SEED, owner.key().as_ref(), node_id.as_ref()], bump)]
    pub node_unbonding: UncheckedAccount<'info>,
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

#[allow(clippy::too_many_arguments)]
pub fn open_node(
    ctx: Context<OpenNode>,
    node_id: [u8; 32],
    prices: NodePrices,
    protocols: u32,
    country: [u8; 2],
    uri: Vec<u8>,
    bond: u64,
    node_rent_recipient: Pubkey,
) -> Result<()> {
    require!(
        ctx.accounts.config.paused_mask & PAUSE_NODE_OPENS == 0,
        DvpnError::OperationPaused
    );
    crate::unbonding::require_no_pending_unbonding(&ctx.accounts.node_unbonding)?;
    require!(
        bond >= ctx.accounts.config.min_node_bond,
        DvpnError::BondBelowMinimum
    );
    let now = Clock::get()?.unix_timestamp;
    let owner = ctx.accounts.owner.key();
    let node_key = ctx.accounts.node.key();
    crate::auth::require_owner_or_grantee(
        &owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_OPEN_NODE,
        &[node_key],
        now,
    )?;
    crate::rent::require_rent_recipient(
        &node_rent_recipient,
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
    let node: &mut Node = &mut ctx.accounts.node;
    node.node_id = node_id;
    node.owner = owner;
    node.status = ListingStatus::Delisted as u8;
    node.listed_at = 0;
    node.channel_price_per_gb = prices.channel_price_per_gb;
    node.channel_price_per_hour = prices.channel_price_per_hour;
    node.lease_price_per_hour = prices.lease_price_per_hour;
    node.protocols = protocols;
    node.country = country;
    crate::uri::write_uri(&mut node.uri, &mut node.uri_len, &uri)?;
    node.bond = bond;
    node.revenue = 0;
    node.fees_accrued = 0;
    node.rent_recipient = node_rent_recipient;
    node.opened_at = now;
    node.bump = ctx.bumps.node;
    node.reserved = [0u8; 32];
    emit!(NodeOpened {
        node: node_key,
        node_id,
        owner: node.owner,
        bond,
        channel_price_per_gb: node.channel_price_per_gb,
        channel_price_per_hour: node.channel_price_per_hour,
        lease_price_per_hour: node.lease_price_per_hour,
        protocols,
        country,
        uri,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct UpdateNode<'info> {
    pub signer: Signer<'info>,
    #[account(mut, seeds = [NODE_SEED, node.owner.as_ref(), node.node_id.as_ref()], bump = node.bump)]
    pub node: Account<'info, Node>,
    #[account(seeds = [GRANT_SEED, node.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
}

pub fn update_node(
    ctx: Context<UpdateNode>,
    prices: Option<NodePrices>,
    protocols: Option<u32>,
    country: Option<[u8; 2]>,
    uri: Option<Vec<u8>>,
) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    crate::auth::require_owner_or_grantee(
        &ctx.accounts.node.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_UPDATE_NODE,
        &[ctx.accounts.node.key()],
        now,
    )?;
    let node_key = ctx.accounts.node.key();
    let node: &mut Node = &mut ctx.accounts.node;
    if let Some(prices) = prices {
        node.channel_price_per_gb = prices.channel_price_per_gb;
        node.channel_price_per_hour = prices.channel_price_per_hour;
        node.lease_price_per_hour = prices.lease_price_per_hour;
    }
    if let Some(protocols) = protocols {
        node.protocols = protocols;
    }
    if let Some(country) = country {
        node.country = country;
    }
    if let Some(uri) = uri {
        crate::uri::write_uri(&mut node.uri, &mut node.uri_len, &uri)?;
    }
    emit!(NodeUpdated {
        node: node_key,
        channel_price_per_gb: node.channel_price_per_gb,
        channel_price_per_hour: node.channel_price_per_hour,
        lease_price_per_hour: node.lease_price_per_hour,
        protocols: node.protocols,
        country: node.country,
        uri: node
            .uri
            .get(..usize::from(node.uri_len))
            .unwrap_or_default()
            .to_vec(),
    });
    Ok(())
}

#[derive(Accounts)]
pub struct ListNode<'info> {
    pub signer: Signer<'info>,
    #[account(mut, seeds = [NODE_SEED, node.owner.as_ref(), node.node_id.as_ref()], bump = node.bump)]
    pub node: Account<'info, Node>,
    #[account(seeds = [GRANT_SEED, node.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
}

pub fn list_node(ctx: Context<ListNode>) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    crate::auth::require_owner_or_grantee(
        &ctx.accounts.node.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_LIST_NODE,
        &[ctx.accounts.node.key()],
        now,
    )?;
    let node = &mut ctx.accounts.node;
    node.status = ListingStatus::Listed as u8;
    node.listed_at = now;
    emit!(NodeListed {
        node: node.key(),
        listed_at: now,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct DelistNode<'info> {
    pub signer: Signer<'info>,
    #[account(mut, seeds = [NODE_SEED, node.owner.as_ref(), node.node_id.as_ref()], bump = node.bump)]
    pub node: Account<'info, Node>,
    #[account(seeds = [GRANT_SEED, node.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
}

pub fn delist_node(ctx: Context<DelistNode>) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    crate::auth::require_owner_or_grantee(
        &ctx.accounts.node.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_DELIST_NODE,
        &[ctx.accounts.node.key()],
        now,
    )?;
    let node = &mut ctx.accounts.node;
    node.status = ListingStatus::Delisted as u8;
    node.listed_at = 0;
    emit!(NodeDelisted { node: node.key() });
    Ok(())
}

#[derive(Accounts)]
pub struct SweepNodeFees<'info> {
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

pub fn sweep_node_fees(ctx: Context<SweepNodeFees>) -> Result<()> {
    let remaining = ctx.remaining_accounts;
    crate::batch::require_sorted_unique(remaining)?;
    crate::batch::require_same_nonempty_len(remaining.len(), remaining.len())?;
    let mut total_fees: u64 = 0;
    for info in remaining {
        let mut node: Account<Node> = Account::try_from(info)?;
        if node.fees_accrued > 0 {
            total_fees = total_fees
                .checked_add(node.fees_accrued)
                .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
            emit!(NodeFeesSwept {
                node: node.key(),
                treasury: ctx.accounts.treasury.key(),
                amount: node.fees_accrued,
            });
            node.fees_accrued = 0;
            node.exit(&crate::ID)?;
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
pub struct CloseNode<'info> {
    pub signer: Signer<'info>,
    #[account(mut)]
    pub rent_payer: Signer<'info>,
    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Box<Account<'info, Config>>,
    #[account(seeds = [GRANT_SEED, node.owner.as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
    pub grant: Option<Account<'info, Grant>>,
    #[account(
        mut,
        close = rent_recipient,
        seeds = [NODE_SEED, node.owner.as_ref(), node.node_id.as_ref()],
        bump = node.bump,
        has_one = owner @ DvpnError::Unauthorized
    )]
    pub node: Box<Account<'info, Node>>,
    #[doc = "CHECK: the node owner; the grant gate validates it against `signer`."]
    #[account(address = node.owner)]
    pub owner: UncheckedAccount<'info>,
    #[doc = "CHECK: receives the Node's rent; pinned to the stored rent recipient."]
    #[account(mut, address = node.rent_recipient)]
    pub rent_recipient: UncheckedAccount<'info>,
    #[account(
        init,
        payer = rent_payer,
        space = 8 + NodeUnbonding::INIT_SPACE,
        seeds = [NODE_UNBONDING_SEED, node.owner.as_ref(), node.node_id.as_ref()],
        bump
    )]
    pub node_unbonding: Account<'info, NodeUnbonding>,
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

pub fn close_node(ctx: Context<CloseNode>, node_unbonding_rent_recipient: Pubkey) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    crate::auth::require_owner_or_grantee(
        &ctx.accounts.node.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_CLOSE_NODE,
        &[ctx.accounts.node.key()],
        now,
    )?;
    crate::rent::require_rent_recipient(
        &node_unbonding_rent_recipient,
        &ctx.accounts.signer.key(),
        &ctx.accounts.rent_payer.key(),
    )?;
    let revenue_paid = ctx.accounts.node.revenue;
    if revenue_paid > 0 {
        crate::transfer::transfer_from_vault(
            &ctx.accounts.token_program,
            &ctx.accounts.vault,
            &ctx.accounts.mint,
            &ctx.accounts.owner_token,
            &ctx.accounts.config.to_account_info(),
            ctx.accounts.config.bump,
            revenue_paid,
        )?;
        ctx.accounts.node.revenue = 0;
    }
    let fees_accrued = ctx.accounts.node.fees_accrued;
    ctx.accounts.config.fees_accrued = ctx
        .accounts
        .config
        .fees_accrued
        .checked_add(fees_accrued)
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
    ctx.accounts.node.fees_accrued = 0;

    let node_unbonding = &mut ctx.accounts.node_unbonding;
    node_unbonding.node_id = ctx.accounts.node.node_id;
    node_unbonding.owner = ctx.accounts.node.owner;
    node_unbonding.bond = ctx.accounts.node.bond;
    node_unbonding.closed_at = now;
    node_unbonding.rent_recipient = node_unbonding_rent_recipient;
    node_unbonding.bump = ctx.bumps.node_unbonding;
    node_unbonding.reserved = [0u8; 32];
    emit!(NodeClosed {
        node: ctx.accounts.node.key(),
        node_id: node_unbonding.node_id,
        owner: node_unbonding.owner,
        bond: node_unbonding.bond,
        unbonding: node_unbonding.key(),
        closed_at: now,
        revenue_paid,
        fees_accrued,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct WithdrawNodeBond<'info> {
    pub signer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(
        mut,
        close = rent_recipient,
        seeds = [NODE_UNBONDING_SEED, node_unbonding.owner.as_ref(), node_unbonding.node_id.as_ref()],
        bump = node_unbonding.bump
    )]
    pub node_unbonding: Account<'info, NodeUnbonding>,
    #[doc = "CHECK: pinned to the stored rent recipient."]
    #[account(mut, address = node_unbonding.rent_recipient)]
    pub rent_recipient: UncheckedAccount<'info>,
    #[doc = "CHECK: the record's owner; the grant gate validates it against `signer`."]
    #[account(address = node_unbonding.owner)]
    pub owner: UncheckedAccount<'info>,
    #[account(seeds = [GRANT_SEED, owner.key().as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
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

pub fn withdraw_node_bond(ctx: Context<WithdrawNodeBond>) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    let node_unbonding = &ctx.accounts.node_unbonding;
    let node_key = match ctx.accounts.grant.as_ref() {
        Some(grant) if grant.target != Pubkey::default() => {
            Pubkey::find_program_address(
                &[
                    NODE_SEED,
                    node_unbonding.owner.as_ref(),
                    node_unbonding.node_id.as_ref(),
                ],
                &crate::ID,
            )
            .0
        }
        _ => Pubkey::default(),
    };
    crate::auth::require_owner_or_grantee(
        &node_unbonding.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_WITHDRAW_NODE_BOND,
        &[node_key, node_unbonding.key()],
        now,
    )?;
    require!(
        crate::deadline::has_elapsed(
            ctx.accounts.node_unbonding.closed_at,
            ctx.accounts.config.unbonding_seconds,
            now
        )?,
        DvpnError::UnbondingNotElapsed
    );
    let bond = ctx.accounts.node_unbonding.bond;
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
    emit!(NodeBondWithdrawn {
        unbonding: ctx.accounts.node_unbonding.key(),
        node_id: ctx.accounts.node_unbonding.node_id,
        owner: ctx.accounts.node_unbonding.owner,
        destination: ctx.accounts.owner_token.key(),
        bond,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct WithdrawNodeRevenue<'info> {
    pub signer: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(
        mut,
        seeds = [NODE_SEED, node.owner.as_ref(), node.node_id.as_ref()],
        bump = node.bump,
        has_one = owner @ DvpnError::Unauthorized
    )]
    pub node: Account<'info, Node>,
    #[doc = "CHECK: the node owner; the grant gate validates it against `signer`."]
    #[account(address = node.owner)]
    pub owner: UncheckedAccount<'info>,
    #[account(seeds = [GRANT_SEED, owner.key().as_ref(), signer.key().as_ref(), grant.target.as_ref()], bump = grant.bump)]
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

pub fn withdraw_node_revenue(ctx: Context<WithdrawNodeRevenue>, amount: u64) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    crate::auth::require_owner_or_grantee(
        &ctx.accounts.node.owner,
        &ctx.accounts.signer.key(),
        ctx.accounts.grant.as_ref(),
        PERM_WITHDRAW_NODE_REVENUE,
        &[ctx.accounts.node.key()],
        now,
    )?;
    require!(amount > 0, DvpnError::ParamOutOfRange);
    require!(
        amount <= ctx.accounts.node.revenue,
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
    ctx.accounts.node.revenue = ctx
        .accounts
        .node
        .revenue
        .checked_sub(amount)
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
    emit!(NodeRevenueWithdrawn {
        node: ctx.accounts.node.key(),
        destination: ctx.accounts.owner_token.key(),
        amount,
        node_revenue: ctx.accounts.node.revenue,
    });
    Ok(())
}
