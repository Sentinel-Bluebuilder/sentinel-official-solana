use anchor_lang::prelude::*;

#[event]
pub struct ConfigInitialized {
    pub authority: Pubkey,
    pub mint: Pubkey,
    pub vault: Pubkey,
    pub treasury: Pubkey,
    pub fee_bps: u16,
    pub min_node_bond: u64,
    pub min_provider_bond: u64,
    pub unbonding_seconds: u32,
    pub channel_close_grace_seconds: u32,
    pub lease_close_grace_seconds: u32,
    pub node_listing_ttl_seconds: u32,
    pub paused_mask: u8,
}

#[event]
pub struct ParamsSet {
    pub fee_bps: u16,
    pub min_node_bond: u64,
    pub min_provider_bond: u64,
    pub unbonding_seconds: u32,
    pub channel_close_grace_seconds: u32,
    pub lease_close_grace_seconds: u32,
    pub node_listing_ttl_seconds: u32,
}

#[event]
pub struct TreasurySet {
    pub treasury: Pubkey,
}

#[event]
pub struct PausedMaskSet {
    pub paused_mask: u8,
}

#[event]
pub struct AuthorityProposed {
    pub pending_authority: Pubkey,
}

#[event]
pub struct AuthorityAccepted {
    pub authority: Pubkey,
}

#[event]
pub struct ConfigFeesSwept {
    pub treasury: Pubkey,
    pub amount: u64,
}

#[event]
pub struct GrantOpened {
    pub grant: Pubkey,
    pub granter: Pubkey,
    pub grantee: Pubkey,
    pub target: Pubkey,
    pub permissions: u64,
    /// Unix seconds.
    pub expires_at: i64,
    pub allowance: u64,
    pub grant_rent_recipient: Pubkey,
}

#[event]
pub struct GrantUpdated {
    pub grant: Pubkey,
    pub granter: Pubkey,
    pub grantee: Pubkey,
    pub target: Pubkey,
    pub permissions: u64,
    /// Unix seconds.
    pub expires_at: i64,
    pub allowance: u64,
}

#[event]
pub struct GrantClosed {
    pub grant: Pubkey,
    pub granter: Pubkey,
    pub grantee: Pubkey,
    pub target: Pubkey,
    pub closer: Pubkey,
}

#[event]
pub struct GrantUsed {
    pub grant: Pubkey,
    pub granter: Pubkey,
    pub grantee: Pubkey,
    pub permission: u64,
    pub target: Pubkey,
}

#[event]
pub struct NodeOpened {
    pub node: Pubkey,
    pub node_id: [u8; 32],
    pub owner: Pubkey,
    pub bond: u64,
    pub channel_price_per_gb: u64,
    pub channel_price_per_hour: u64,
    pub lease_price_per_hour: u64,
    pub protocols: u32,
    pub country: [u8; 2],
    pub uri: Vec<u8>,
}

#[event]
pub struct NodeUpdated {
    pub node: Pubkey,
    pub channel_price_per_gb: u64,
    pub channel_price_per_hour: u64,
    pub lease_price_per_hour: u64,
    pub protocols: u32,
    pub country: [u8; 2],
    pub uri: Vec<u8>,
}

#[event]
pub struct NodeListed {
    pub node: Pubkey,
    /// Unix seconds.
    pub listed_at: i64,
}

#[event]
pub struct NodeDelisted {
    pub node: Pubkey,
}

#[event]
pub struct NodeFeesSwept {
    pub node: Pubkey,
    pub treasury: Pubkey,
    pub amount: u64,
}

#[event]
pub struct NodeClosed {
    pub node: Pubkey,
    pub node_id: [u8; 32],
    pub owner: Pubkey,
    pub bond: u64,
    pub unbonding: Pubkey,
    /// Unix seconds.
    pub closed_at: i64,
    pub revenue_paid: u64,
    pub fees_accrued: u64,
}

#[event]
pub struct NodeBondWithdrawn {
    pub unbonding: Pubkey,
    pub node_id: [u8; 32],
    pub owner: Pubkey,
    pub destination: Pubkey,
    pub bond: u64,
}

#[event]
pub struct NodeRevenueWithdrawn {
    pub node: Pubkey,
    pub destination: Pubkey,
    pub amount: u64,
    pub node_revenue: u64,
}

#[event]
pub struct ProviderOpened {
    pub provider: Pubkey,
    pub provider_id: [u8; 32],
    pub owner: Pubkey,
    pub bond: u64,
    pub uri: Vec<u8>,
}

#[event]
pub struct ProviderUpdated {
    pub provider: Pubkey,
    pub uri: Vec<u8>,
}

#[event]
pub struct ProviderListed {
    pub provider: Pubkey,
}

#[event]
pub struct ProviderDelisted {
    pub provider: Pubkey,
}

#[event]
pub struct ProviderRevenueWithdrawn {
    pub provider: Pubkey,
    pub destination: Pubkey,
    pub amount: u64,
    pub provider_revenue: u64,
}

#[event]
pub struct ProviderBalanceWithdrawn {
    pub provider: Pubkey,
    pub destination: Pubkey,
    pub amount: u64,
}

#[event]
pub struct ProviderFeesSwept {
    pub provider: Pubkey,
    pub treasury: Pubkey,
    pub amount: u64,
}

#[event]
pub struct ProviderClosed {
    pub provider: Pubkey,
    pub provider_id: [u8; 32],
    pub owner: Pubkey,
    pub bond: u64,
    pub unbonding: Pubkey,
    /// Unix seconds.
    pub closed_at: i64,
    pub revenue_paid: u64,
    pub balance_paid: u64,
    pub fees_accrued: u64,
}

#[event]
pub struct ProviderBondWithdrawn {
    pub unbonding: Pubkey,
    pub provider_id: [u8; 32],
    pub owner: Pubkey,
    pub destination: Pubkey,
    pub bond: u64,
}

#[event]
pub struct PlanOpened {
    pub plan: Pubkey,
    pub provider: Pubkey,
    pub plan_id: [u8; 32],
    pub price: u64,
    pub duration_seconds: u32,
    pub data_cap_gb: u32,
    pub max_connections: u8,
}

#[event]
pub struct PlanUpdated {
    pub plan: Pubkey,
    pub provider: Pubkey,
    pub price: u64,
    pub duration_seconds: u32,
    pub data_cap_gb: u32,
    pub max_connections: u8,
}

#[event]
pub struct PlanListed {
    pub plan: Pubkey,
}

#[event]
pub struct PlanDelisted {
    pub plan: Pubkey,
}

#[event]
pub struct PlanClosed {
    pub plan: Pubkey,
    pub provider: Pubkey,
}

#[event]
pub struct SubscriptionOpened {
    pub subscription: Pubkey,
    pub owner: Pubkey,
    pub plan: Pubkey,
    pub provider: Pubkey,
    pub price: u64,
    pub fee: u64,
    /// Unix seconds.
    pub expires_at: i64,
    pub data_cap_gb: u32,
    pub max_connections: u8,
    pub subscription_rent_recipient: Pubkey,
    pub provider_revenue: u64,
    pub provider_fees_accrued: u64,
}

#[event]
pub struct SubscriptionClosed {
    pub subscription: Pubkey,
    pub owner: Pubkey,
    pub plan: Pubkey,
    pub provider: Pubkey,
    pub closer: Pubkey,
    pub close_reason: u8,
    pub early: bool,
}

#[event]
pub struct LeaseOpened {
    pub lease: Pubkey,
    pub lease_id: [u8; 32],
    pub provider: Pubkey,
    pub node: Pubkey,
    pub owner: Pubkey,
    pub opener: Pubkey,
    pub mode: u8,
    pub voucher_signer: Pubkey,
    pub rate_per_hour: u64,
    pub escrow: u64,
    pub from_balance: u64,
    pub from_revenue: u64,
    pub provider_balance: u64,
    pub provider_revenue: u64,
    pub allowance: u64,
}

#[event]
pub struct LeaseFunded {
    pub lease: Pubkey,
    pub funder: Pubkey,
    pub amount: u64,
    pub escrow: u64,
    pub stream_base: u64,
    /// Unix seconds.
    pub stream_started_at: i64,
    pub from_balance: u64,
    pub from_revenue: u64,
    pub provider_balance: u64,
    pub provider_revenue: u64,
    pub allowance: u64,
}

#[event]
pub struct LeaseVoucherRedeemed {
    pub lease: Pubkey,
    pub node: Pubkey,
    pub gross_amount: u64,
    pub fee: u64,
    pub redeemed_total: u64,
    pub node_revenue: u64,
    pub node_fees_accrued: u64,
}

#[event]
pub struct LeaseStreamRedeemed {
    pub lease: Pubkey,
    pub node: Pubkey,
    pub gross_amount: u64,
    pub fee: u64,
    pub redeemed_total: u64,
    pub node_revenue: u64,
    pub node_fees_accrued: u64,
}

#[event]
pub struct LeaseCloseStarted {
    pub lease: Pubkey,
    /// Unix seconds.
    pub close_started_at: i64,
}

#[event]
pub struct LeaseClosed {
    pub lease: Pubkey,
    pub provider: Pubkey,
    pub node: Pubkey,
    pub owner: Pubkey,
    pub mode: u8,
    pub escrow: u64,
    pub redeemed: u64,
    pub returned: u64,
    pub forfeited: u64,
    pub provider_balance: u64,
}

#[event]
pub struct UserCreated {
    pub user: Pubkey,
    pub owner: Pubkey,
}

#[event]
pub struct UserBalanceWithdrawn {
    pub user: Pubkey,
    pub owner: Pubkey,
    pub destination: Pubkey,
    pub amount: u64,
    pub closed: bool,
}

#[event]
pub struct ChannelOpened {
    pub channel: Pubkey,
    pub channel_id: [u8; 32],
    pub owner: Pubkey,
    pub node: Pubkey,
    pub opener: Pubkey,
    pub mode: u8,
    pub voucher_signer: Pubkey,
    pub rate_per_hour: u64,
    pub escrow: u64,
    pub from_balance: u64,
    pub user_balance: u64,
    pub allowance: u64,
}

#[event]
pub struct ChannelFunded {
    pub channel: Pubkey,
    pub funder: Pubkey,
    pub amount: u64,
    pub escrow: u64,
    pub stream_base: u64,
    /// Unix seconds.
    pub stream_started_at: i64,
    pub from_balance: u64,
    pub user_balance: u64,
    pub allowance: u64,
}

#[event]
pub struct ChannelVoucherRedeemed {
    pub channel: Pubkey,
    pub node: Pubkey,
    pub gross_amount: u64,
    pub fee: u64,
    pub redeemed_total: u64,
    pub node_revenue: u64,
    pub node_fees_accrued: u64,
}

#[event]
pub struct ChannelStreamRedeemed {
    pub channel: Pubkey,
    pub node: Pubkey,
    pub gross_amount: u64,
    pub fee: u64,
    pub redeemed_total: u64,
    pub node_revenue: u64,
    pub node_fees_accrued: u64,
}

#[event]
pub struct ChannelCloseStarted {
    pub channel: Pubkey,
    /// Unix seconds.
    pub close_started_at: i64,
}

#[event]
pub struct ChannelClosed {
    pub channel: Pubkey,
    pub owner: Pubkey,
    pub node: Pubkey,
    pub mode: u8,
    pub escrow: u64,
    pub redeemed: u64,
    pub returned: u64,
    pub forfeited: u64,
    pub user_balance: u64,
}
