use spl_token_2022_interface::extension::ExtensionType;

pub const MAX_FEE_BPS: u16 = 1000;

pub const MIN_UNBONDING_SECONDS: u32 = 3_600;

pub const MAX_UNBONDING_SECONDS: u32 = 7_776_000;

pub const MIN_GRACE_SECONDS: u32 = 300;

pub const MAX_GRACE_SECONDS: u32 = 7_776_000;

pub const MIN_NODE_LISTING_TTL_SECONDS: u32 = 300;

pub const MAX_NODE_LISTING_TTL_SECONDS: u32 = 7_776_000;

pub const ALLOWANCE_UNLIMITED: u64 = u64::MAX;

pub const MAX_URI_BYTES: usize = 128;

pub const PAUSE_NODE_OPENS: u8 = 1 << 0;

pub const PAUSE_PROVIDER_OPENS: u8 = 1 << 1;

pub const PAUSE_PLAN_OPENS: u8 = 1 << 2;

pub const PAUSE_SUBSCRIPTION_OPENS: u8 = 1 << 3;

pub const PAUSE_LEASE_OPENS: u8 = 1 << 4;

pub const PAUSE_CHANNEL_OPENS: u8 = 1 << 5;

pub const PAUSE_MASK: u8 = 0x3F;

pub const ALLOWED_MINT_EXTENSIONS: &[ExtensionType] = &[
    ExtensionType::ConfidentialTransferMint,
    ExtensionType::MetadataPointer,
    ExtensionType::TokenMetadata,
];
