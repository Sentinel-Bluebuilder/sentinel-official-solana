use distributor_core::consts::{LEAF_TAG, TREE_DOMAIN_SEPARATOR};
use distributor_core::hash::keccak256;
use distributor_merkle::leaf::{LeafFields, encode_leaf, leaf_hash};

fn sample(address: &str) -> LeafFields<'_> {
    LeafFields {
        shard_id: 0x0102,
        leaf_index: 0x03040506,
        compressed_public_key: [0x02u8; 33],
        bech32_address: address,
        liquid_amount: 1_000_000,
        locked_amount: 250_000,
        vesting_schedule_hash: [0xabu8; 32],
    }
}

#[test]
fn encode_leaf_layout_is_pinned_little_endian() {
    let address = "sent1qqqq";
    let fields = sample(address);
    let mut want = Vec::new();
    want.push(LEAF_TAG);
    want.extend_from_slice(TREE_DOMAIN_SEPARATOR);
    want.extend_from_slice(&0x0102u16.to_le_bytes());
    want.extend_from_slice(&0x03040506u32.to_le_bytes());
    want.extend_from_slice(&[0x02u8; 33]);
    want.extend_from_slice(&u32::try_from(address.len()).unwrap().to_le_bytes());
    want.extend_from_slice(address.as_bytes());
    want.extend_from_slice(&1_000_000u64.to_le_bytes());
    want.extend_from_slice(&250_000u64.to_le_bytes());
    want.extend_from_slice(&[0xabu8; 32]);
    assert_eq!(encode_leaf(&fields), want);
}

#[test]
fn leaf_hash_is_keccak_of_preimage() {
    let fields = sample("sent1qqqq");
    assert_eq!(leaf_hash(&fields), keccak256(&[&encode_leaf(&fields)]));
}

#[test]
fn distinct_fields_produce_distinct_hashes() {
    let base = leaf_hash(&sample("sent1qqqq"));
    let mut fields = sample("sent1qqqq");
    fields.liquid_amount += 1;
    assert_ne!(leaf_hash(&fields), base);
    let mut second_fields = sample("sent1qqqq");
    second_fields.shard_id += 1;
    assert_ne!(leaf_hash(&second_fields), base);
}
