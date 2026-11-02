use crate::consts::{SHARD_COUNT, SHARD_DOMAIN_SEPARATOR};
use crate::hash::keccak256;

#[must_use]
pub fn shard_id(address_bytes: &[u8; 20]) -> u16 {
    let hash = keccak256(&[SHARD_DOMAIN_SEPARATOR, address_bytes]);
    u16::from_le_bytes([hash[0], hash[1]]) % SHARD_COUNT
}
