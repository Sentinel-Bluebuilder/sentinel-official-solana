use distributor_core::consts::{LEAF_TAG, TREE_DOMAIN_SEPARATOR};
use distributor_core::hash::keccak256;

pub const UNREVEALED_PUBKEY: [u8; 33] = [0u8; 33];

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct LeafFields<'a> {
    pub shard_id: u16,
    pub leaf_index: u32,
    pub compressed_public_key: [u8; 33],
    pub bech32_address: &'a str,
    pub liquid_amount: u64,
    pub locked_amount: u64,
    pub vesting_schedule_hash: [u8; 32],
}

#[must_use]
#[allow(clippy::expect_used)]
pub fn encode_leaf(fields: &LeafFields) -> Vec<u8> {
    const FIXED_BYTES: usize = 1 + 2 + 4 + 33 + 4 + 8 + 8 + 32;

    let address = fields.bech32_address.as_bytes();
    let mut buf = Vec::with_capacity(
        FIXED_BYTES
            .saturating_add(TREE_DOMAIN_SEPARATOR.len())
            .saturating_add(address.len()),
    );
    buf.push(LEAF_TAG);
    buf.extend_from_slice(TREE_DOMAIN_SEPARATOR);
    buf.extend_from_slice(&fields.shard_id.to_le_bytes());
    buf.extend_from_slice(&fields.leaf_index.to_le_bytes());
    buf.extend_from_slice(&fields.compressed_public_key);
    buf.extend_from_slice(
        &u32::try_from(address.len())
            .expect("bech32 address fits in u32")
            .to_le_bytes(),
    );
    buf.extend_from_slice(address);
    buf.extend_from_slice(&fields.liquid_amount.to_le_bytes());
    buf.extend_from_slice(&fields.locked_amount.to_le_bytes());
    buf.extend_from_slice(&fields.vesting_schedule_hash);
    buf
}

#[must_use]
#[allow(clippy::expect_used)]
pub fn leaf_hash(fields: &LeafFields) -> [u8; 32] {
    let address = fields.bech32_address.as_bytes();
    let address_len = u32::try_from(address.len())
        .expect("bech32 address fits in u32")
        .to_le_bytes();
    keccak256(&[
        &[LEAF_TAG],
        TREE_DOMAIN_SEPARATOR,
        &fields.shard_id.to_le_bytes(),
        &fields.leaf_index.to_le_bytes(),
        &fields.compressed_public_key,
        &address_len,
        address,
        &fields.liquid_amount.to_le_bytes(),
        &fields.locked_amount.to_le_bytes(),
        &fields.vesting_schedule_hash,
    ])
}
