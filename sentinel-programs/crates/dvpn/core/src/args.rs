use anchor_lang::prelude::*;

#[derive(AnchorSerialize, AnchorDeserialize, Clone, PartialEq, Eq, Debug, Default)]
pub struct InitialConfigParams {
    pub fee_bps: u16,
    pub min_node_bond: u64,
    pub min_provider_bond: u64,
    pub unbonding_seconds: u32,
    pub channel_close_grace_seconds: u32,
    pub lease_close_grace_seconds: u32,
    pub node_listing_ttl_seconds: u32,
    pub paused_mask: u8,
}

#[derive(AnchorSerialize, AnchorDeserialize, Clone, PartialEq, Eq, Debug, Default)]
pub struct ConfigParamsUpdate {
    pub fee_bps: Option<u16>,
    pub min_node_bond: Option<u64>,
    pub min_provider_bond: Option<u64>,
    pub unbonding_seconds: Option<u32>,
    pub channel_close_grace_seconds: Option<u32>,
    pub lease_close_grace_seconds: Option<u32>,
    pub node_listing_ttl_seconds: Option<u32>,
}

#[derive(AnchorSerialize, AnchorDeserialize, Clone, Copy, PartialEq, Eq, Debug, Default)]
pub struct NodePrices {
    pub channel_price_per_gb: u64,
    pub channel_price_per_hour: u64,
    pub lease_price_per_hour: u64,
}
