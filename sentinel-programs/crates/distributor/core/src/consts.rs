pub const TREE_DOMAIN_SEPARATOR: &[u8] = b"sentinel-migration/v1";
pub const SHARD_DOMAIN_SEPARATOR: &[u8] = b"sentinel-migration/shard/v1";
pub const ROOTS_DOMAIN_SEPARATOR: &[u8] = b"sentinel-migration/roots/v1";
pub const LEAF_TAG: u8 = 0x00;
pub const NODE_TAG: u8 = 0x01;
pub const HRP: &str = "sent";
pub const SHARD_COUNT: u16 = 128;
pub const DECIMALS: u8 = 6;
pub const CLAIM_COMPUTE_UNIT_LIMIT: u32 = 400_000;
pub const EMPTY_SHARD_ROOT: [u8; 32] = [0u8; 32];

const _: () = assert!(
    SHARD_COUNT.is_power_of_two(),
    "SHARD_COUNT must be a power of two"
);
