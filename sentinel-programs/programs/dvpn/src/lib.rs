#![deny(
    clippy::unwrap_used,
    clippy::expect_used,
    clippy::panic,
    clippy::indexing_slicing,
    clippy::arithmetic_side_effects
)]

use anchor_lang::prelude::*;
use dvpn_core::args::{ConfigParamsUpdate, InitialConfigParams, NodePrices};
use dvpn_core::enums::{
    ListingStatus, PositionMode, PositionStatus, PositionTerms, SubscriptionCloseReason,
};
use dvpn_core::voucher::Voucher;
use instructions::*;

pub mod auth;
pub mod batch;
pub mod deadline;
pub mod error;
pub mod events;
pub mod fee;
pub mod instructions;
pub mod mint;
pub mod rent;
pub mod state;
pub mod stream;
pub mod transfer;
pub mod unbonding;
pub mod uri;
pub mod verify;

declare_id!("SentineLDVPN1111111111111111111111111111111");

#[constant]
pub const POSITION_MODE_VOUCHER: u8 = PositionMode::Voucher as u8;
#[constant]
pub const POSITION_MODE_STREAM: u8 = PositionMode::Stream as u8;

#[constant]
pub const LISTING_STATUS_DELISTED: u8 = ListingStatus::Delisted as u8;
#[constant]
pub const LISTING_STATUS_LISTED: u8 = ListingStatus::Listed as u8;
#[constant]
pub const POSITION_STATUS_OPEN: u8 = PositionStatus::Open as u8;
#[constant]
pub const POSITION_STATUS_CLOSING: u8 = PositionStatus::Closing as u8;

#[constant]
pub const SUBSCRIPTION_CLOSE_REASON_OWNER: u8 = SubscriptionCloseReason::Owner as u8;
#[constant]
pub const SUBSCRIPTION_CLOSE_REASON_EXPIRED: u8 = SubscriptionCloseReason::Expired as u8;
#[constant]
pub const SUBSCRIPTION_CLOSE_REASON_PROVIDER: u8 = SubscriptionCloseReason::Provider as u8;
#[constant]
pub const SUBSCRIPTION_CLOSE_REASON_GRANTEE: u8 = SubscriptionCloseReason::Grantee as u8;

#[constant]
pub const ALLOWANCE_UNLIMITED: u64 = dvpn_core::constants::ALLOWANCE_UNLIMITED;

#[constant]
pub const PERM_OPEN_NODE: u64 = dvpn_core::permissions::PERM_OPEN_NODE;
#[constant]
pub const PERM_UPDATE_NODE: u64 = dvpn_core::permissions::PERM_UPDATE_NODE;
#[constant]
pub const PERM_LIST_NODE: u64 = dvpn_core::permissions::PERM_LIST_NODE;
#[constant]
pub const PERM_DELIST_NODE: u64 = dvpn_core::permissions::PERM_DELIST_NODE;
#[constant]
pub const PERM_CLOSE_NODE: u64 = dvpn_core::permissions::PERM_CLOSE_NODE;
#[constant]
pub const PERM_WITHDRAW_NODE_BOND: u64 = dvpn_core::permissions::PERM_WITHDRAW_NODE_BOND;
#[constant]
pub const PERM_WITHDRAW_NODE_REVENUE: u64 = dvpn_core::permissions::PERM_WITHDRAW_NODE_REVENUE;
#[constant]
pub const PERM_OPEN_PROVIDER: u64 = dvpn_core::permissions::PERM_OPEN_PROVIDER;
#[constant]
pub const PERM_UPDATE_PROVIDER: u64 = dvpn_core::permissions::PERM_UPDATE_PROVIDER;
#[constant]
pub const PERM_LIST_PROVIDER: u64 = dvpn_core::permissions::PERM_LIST_PROVIDER;
#[constant]
pub const PERM_DELIST_PROVIDER: u64 = dvpn_core::permissions::PERM_DELIST_PROVIDER;
#[constant]
pub const PERM_CLOSE_PROVIDER: u64 = dvpn_core::permissions::PERM_CLOSE_PROVIDER;
#[constant]
pub const PERM_WITHDRAW_PROVIDER_BOND: u64 = dvpn_core::permissions::PERM_WITHDRAW_PROVIDER_BOND;
#[constant]
pub const PERM_WITHDRAW_PROVIDER_REVENUE: u64 =
    dvpn_core::permissions::PERM_WITHDRAW_PROVIDER_REVENUE;
#[constant]
pub const PERM_WITHDRAW_PROVIDER_BALANCE: u64 =
    dvpn_core::permissions::PERM_WITHDRAW_PROVIDER_BALANCE;
#[constant]
pub const PERM_OPEN_PLAN: u64 = dvpn_core::permissions::PERM_OPEN_PLAN;
#[constant]
pub const PERM_UPDATE_PLAN: u64 = dvpn_core::permissions::PERM_UPDATE_PLAN;
#[constant]
pub const PERM_LIST_PLAN: u64 = dvpn_core::permissions::PERM_LIST_PLAN;
#[constant]
pub const PERM_DELIST_PLAN: u64 = dvpn_core::permissions::PERM_DELIST_PLAN;
#[constant]
pub const PERM_CLOSE_PLAN: u64 = dvpn_core::permissions::PERM_CLOSE_PLAN;
#[constant]
pub const PERM_OPEN_LEASE: u64 = dvpn_core::permissions::PERM_OPEN_LEASE;
#[constant]
pub const PERM_FUND_LEASES: u64 = dvpn_core::permissions::PERM_FUND_LEASES;
#[constant]
pub const PERM_START_CLOSE_LEASE: u64 = dvpn_core::permissions::PERM_START_CLOSE_LEASE;
#[constant]
pub const PERM_CLOSE_LEASE: u64 = dvpn_core::permissions::PERM_CLOSE_LEASE;
#[constant]
pub const PERM_OPEN_CHANNEL: u64 = dvpn_core::permissions::PERM_OPEN_CHANNEL;
#[constant]
pub const PERM_FUND_CHANNELS: u64 = dvpn_core::permissions::PERM_FUND_CHANNELS;
#[constant]
pub const PERM_START_CLOSE_CHANNEL: u64 = dvpn_core::permissions::PERM_START_CLOSE_CHANNEL;
#[constant]
pub const PERM_CLOSE_CHANNEL: u64 = dvpn_core::permissions::PERM_CLOSE_CHANNEL;
#[constant]
pub const PERM_WITHDRAW_USER_BALANCE: u64 = dvpn_core::permissions::PERM_WITHDRAW_USER_BALANCE;
#[constant]
pub const PERM_OPEN_SUBSCRIPTION: u64 = dvpn_core::permissions::PERM_OPEN_SUBSCRIPTION;
#[constant]
pub const PERM_CLOSE_SUBSCRIPTION: u64 = dvpn_core::permissions::PERM_CLOSE_SUBSCRIPTION;
#[constant]
pub const PERM_OPEN_FREE_SUBSCRIPTION: u64 = dvpn_core::permissions::PERM_OPEN_FREE_SUBSCRIPTION;
#[constant]
pub const PERM_CLOSE_FREE_SUBSCRIPTION: u64 = dvpn_core::permissions::PERM_CLOSE_FREE_SUBSCRIPTION;
#[constant]
pub const PERM_ALL: u64 = dvpn_core::permissions::PERM_ALL;

#[constant]
pub const PAUSE_NODE_OPENS: u8 = dvpn_core::constants::PAUSE_NODE_OPENS;
#[constant]
pub const PAUSE_PROVIDER_OPENS: u8 = dvpn_core::constants::PAUSE_PROVIDER_OPENS;
#[constant]
pub const PAUSE_PLAN_OPENS: u8 = dvpn_core::constants::PAUSE_PLAN_OPENS;
#[constant]
pub const PAUSE_SUBSCRIPTION_OPENS: u8 = dvpn_core::constants::PAUSE_SUBSCRIPTION_OPENS;
#[constant]
pub const PAUSE_LEASE_OPENS: u8 = dvpn_core::constants::PAUSE_LEASE_OPENS;
#[constant]
pub const PAUSE_CHANNEL_OPENS: u8 = dvpn_core::constants::PAUSE_CHANNEL_OPENS;
#[constant]
pub const PAUSE_MASK: u8 = dvpn_core::constants::PAUSE_MASK;

#[program]
pub mod dvpn {
    use super::*;

    #[allow(clippy::too_many_arguments)]
    pub fn initialize_config(
        ctx: Context<InitializeConfig>,
        fee_bps: u16,
        min_node_bond: u64,
        min_provider_bond: u64,
        unbonding_seconds: u32,
        channel_close_grace_seconds: u32,
        lease_close_grace_seconds: u32,
        node_listing_ttl_seconds: u32,
        paused_mask: u8,
    ) -> Result<()> {
        instructions::initialize_config(
            ctx,
            InitialConfigParams {
                fee_bps,
                min_node_bond,
                min_provider_bond,
                unbonding_seconds,
                channel_close_grace_seconds,
                lease_close_grace_seconds,
                node_listing_ttl_seconds,
                paused_mask,
            },
        )
    }

    #[allow(clippy::too_many_arguments)]
    pub fn set_params(
        ctx: Context<SetParams>,
        fee_bps: Option<u16>,
        min_node_bond: Option<u64>,
        min_provider_bond: Option<u64>,
        unbonding_seconds: Option<u32>,
        channel_close_grace_seconds: Option<u32>,
        lease_close_grace_seconds: Option<u32>,
        node_listing_ttl_seconds: Option<u32>,
    ) -> Result<()> {
        instructions::set_params(
            ctx,
            ConfigParamsUpdate {
                fee_bps,
                min_node_bond,
                min_provider_bond,
                unbonding_seconds,
                channel_close_grace_seconds,
                lease_close_grace_seconds,
                node_listing_ttl_seconds,
            },
        )
    }

    pub fn set_treasury(ctx: Context<SetTreasury>) -> Result<()> {
        instructions::set_treasury(ctx)
    }

    pub fn set_paused_mask(ctx: Context<SetPausedMask>, paused_mask: u8) -> Result<()> {
        instructions::set_paused_mask(ctx, paused_mask)
    }

    pub fn propose_authority(ctx: Context<ProposeAuthority>, new_authority: Pubkey) -> Result<()> {
        instructions::propose_authority(ctx, new_authority)
    }

    pub fn accept_authority(ctx: Context<AcceptAuthority>) -> Result<()> {
        instructions::accept_authority(ctx)
    }

    pub fn sweep_config_fees(ctx: Context<SweepConfigFees>) -> Result<()> {
        instructions::sweep_config_fees(ctx)
    }

    pub fn open_grant(
        ctx: Context<OpenGrant>,
        grantee: Pubkey,
        target: Pubkey,
        permissions: u64,
        expires_at: i64,
        allowance: u64,
        grant_rent_recipient: Pubkey,
    ) -> Result<()> {
        instructions::open_grant(
            ctx,
            grantee,
            target,
            permissions,
            expires_at,
            allowance,
            grant_rent_recipient,
        )
    }

    pub fn update_grant(
        ctx: Context<UpdateGrant>,
        permissions: Option<u64>,
        expires_at: Option<i64>,
        allowance: Option<u64>,
    ) -> Result<()> {
        instructions::update_grant(ctx, permissions, expires_at, allowance)
    }

    pub fn close_grant(ctx: Context<CloseGrant>) -> Result<()> {
        instructions::close_grant(ctx)
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
        instructions::open_node(
            ctx,
            node_id,
            prices,
            protocols,
            country,
            uri,
            bond,
            node_rent_recipient,
        )
    }

    pub fn update_node(
        ctx: Context<UpdateNode>,
        prices: Option<NodePrices>,
        protocols: Option<u32>,
        country: Option<[u8; 2]>,
        uri: Option<Vec<u8>>,
    ) -> Result<()> {
        instructions::update_node(ctx, prices, protocols, country, uri)
    }

    pub fn list_node(ctx: Context<ListNode>) -> Result<()> {
        instructions::list_node(ctx)
    }

    pub fn delist_node(ctx: Context<DelistNode>) -> Result<()> {
        instructions::delist_node(ctx)
    }

    pub fn sweep_node_fees(ctx: Context<SweepNodeFees>) -> Result<()> {
        instructions::sweep_node_fees(ctx)
    }

    pub fn close_node(
        ctx: Context<CloseNode>,
        node_unbonding_rent_recipient: Pubkey,
    ) -> Result<()> {
        instructions::close_node(ctx, node_unbonding_rent_recipient)
    }

    pub fn withdraw_node_bond(ctx: Context<WithdrawNodeBond>) -> Result<()> {
        instructions::withdraw_node_bond(ctx)
    }

    pub fn withdraw_node_revenue(ctx: Context<WithdrawNodeRevenue>, amount: u64) -> Result<()> {
        instructions::withdraw_node_revenue(ctx, amount)
    }

    pub fn open_provider(
        ctx: Context<OpenProvider>,
        provider_id: [u8; 32],
        uri: Vec<u8>,
        bond: u64,
        provider_rent_recipient: Pubkey,
    ) -> Result<()> {
        instructions::open_provider(ctx, provider_id, uri, bond, provider_rent_recipient)
    }

    pub fn update_provider(ctx: Context<UpdateProvider>, uri: Option<Vec<u8>>) -> Result<()> {
        instructions::update_provider(ctx, uri)
    }

    pub fn list_provider(ctx: Context<ListProvider>) -> Result<()> {
        instructions::list_provider(ctx)
    }

    pub fn delist_provider(ctx: Context<DelistProvider>) -> Result<()> {
        instructions::delist_provider(ctx)
    }

    pub fn withdraw_provider_revenue(
        ctx: Context<WithdrawProviderRevenue>,
        amount: u64,
    ) -> Result<()> {
        instructions::withdraw_provider_revenue(ctx, amount)
    }

    pub fn withdraw_provider_balance(ctx: Context<WithdrawProviderBalance>) -> Result<()> {
        instructions::withdraw_provider_balance(ctx)
    }

    pub fn sweep_provider_fees(ctx: Context<SweepProviderFees>) -> Result<()> {
        instructions::sweep_provider_fees(ctx)
    }

    pub fn close_provider(
        ctx: Context<CloseProvider>,
        provider_unbonding_rent_recipient: Pubkey,
    ) -> Result<()> {
        instructions::close_provider(ctx, provider_unbonding_rent_recipient)
    }

    pub fn withdraw_provider_bond(ctx: Context<WithdrawProviderBond>) -> Result<()> {
        instructions::withdraw_provider_bond(ctx)
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
        instructions::open_plan(
            ctx,
            plan_id,
            price,
            duration_seconds,
            data_cap_gb,
            max_connections,
            plan_rent_recipient,
        )
    }

    pub fn update_plan(
        ctx: Context<UpdatePlan>,
        price: Option<u64>,
        duration_seconds: Option<u32>,
        data_cap_gb: Option<u32>,
        max_connections: Option<u8>,
    ) -> Result<()> {
        instructions::update_plan(ctx, price, duration_seconds, data_cap_gb, max_connections)
    }

    pub fn list_plan(ctx: Context<ListPlan>) -> Result<()> {
        instructions::list_plan(ctx)
    }

    pub fn delist_plan(ctx: Context<DelistPlan>) -> Result<()> {
        instructions::delist_plan(ctx)
    }

    pub fn close_plan(ctx: Context<ClosePlan>) -> Result<()> {
        instructions::close_plan(ctx)
    }

    pub fn open_subscription(
        ctx: Context<OpenSubscription>,
        expected_price: u64,
        expected_duration_seconds: u32,
        expected_data_cap_gb: u32,
        expected_max_connections: u8,
        subscription_rent_recipient: Pubkey,
    ) -> Result<()> {
        instructions::open_subscription(
            ctx,
            expected_price,
            expected_duration_seconds,
            expected_data_cap_gb,
            expected_max_connections,
            subscription_rent_recipient,
        )
    }

    pub fn close_subscription(ctx: Context<CloseSubscription>) -> Result<()> {
        instructions::close_subscription(ctx)
    }

    pub fn withdraw_user_balance(ctx: Context<WithdrawUserBalance>) -> Result<()> {
        instructions::withdraw_user_balance(ctx)
    }

    pub fn open_channel(
        ctx: Context<OpenChannel>,
        channel_id: [u8; 32],
        escrow: u64,
        terms: PositionTerms,
        channel_rent_recipient: Pubkey,
        user_rent_recipient: Pubkey,
    ) -> Result<()> {
        instructions::open_channel(
            ctx,
            channel_id,
            escrow,
            terms,
            channel_rent_recipient,
            user_rent_recipient,
        )
    }

    pub fn fund_channels(ctx: Context<FundChannels>, amounts: Vec<u64>) -> Result<()> {
        instructions::fund_channels(ctx, amounts)
    }

    pub fn redeem_channel_vouchers(
        ctx: Context<RedeemChannelVouchers>,
        vouchers: Vec<Voucher>,
    ) -> Result<()> {
        instructions::redeem_channel_vouchers(ctx, vouchers)
    }

    pub fn redeem_channel_streams(ctx: Context<RedeemChannelStreams>) -> Result<()> {
        instructions::redeem_channel_streams(ctx)
    }

    pub fn start_close_channel(ctx: Context<StartCloseChannel>) -> Result<()> {
        instructions::start_close_channel(ctx)
    }

    pub fn close_channel<'info>(ctx: Context<'info, CloseChannel<'info>>) -> Result<()> {
        instructions::close_channel(ctx)
    }

    pub fn open_lease(
        ctx: Context<OpenLease>,
        lease_id: [u8; 32],
        escrow: u64,
        terms: PositionTerms,
        lease_rent_recipient: Pubkey,
    ) -> Result<()> {
        instructions::open_lease(ctx, lease_id, escrow, terms, lease_rent_recipient)
    }

    pub fn fund_leases(ctx: Context<FundLeases>, amounts: Vec<u64>) -> Result<()> {
        instructions::fund_leases(ctx, amounts)
    }

    pub fn redeem_lease_vouchers(
        ctx: Context<RedeemLeaseVouchers>,
        vouchers: Vec<Voucher>,
    ) -> Result<()> {
        instructions::redeem_lease_vouchers(ctx, vouchers)
    }

    pub fn redeem_lease_streams(ctx: Context<RedeemLeaseStreams>) -> Result<()> {
        instructions::redeem_lease_streams(ctx)
    }

    pub fn start_close_lease(ctx: Context<StartCloseLease>) -> Result<()> {
        instructions::start_close_lease(ctx)
    }

    pub fn close_lease<'info>(ctx: Context<'info, CloseLease<'info>>) -> Result<()> {
        instructions::close_lease(ctx)
    }
}
