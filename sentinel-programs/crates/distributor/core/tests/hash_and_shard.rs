use distributor_core::consts::SHARD_COUNT;
use distributor_core::hash::{hex_lower, keccak256};
use distributor_core::shard::shard_id;

#[test]
fn keccak_matches_known_vector() {
    let got = keccak256(&[]);
    let want =
        hex::decode("c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470").unwrap();
    assert_eq!(&got[..], &want[..]);
}

#[test]
fn hex_lower_is_lowercase_and_full_width() {
    assert_eq!(hex_lower(&[0x00, 0x0a, 0xff]), "000aff");
    assert_eq!(hex_lower(&[]), "");
}

#[test]
fn shard_id_is_in_range_and_deterministic() {
    let address_bytes = [0x42u8; 20];
    assert_eq!(shard_id(&address_bytes), shard_id(&address_bytes));
    assert!(shard_id(&address_bytes) < SHARD_COUNT);
}

#[test]
fn hash160_is_ripemd160_of_sha256() {
    use distributor_core::hash::hash160;
    let public_key =
        hex::decode("0250863ad64a87ae8a2fe83c1af1a8403cb53f53e486d8511dad8a04887e5b2352").unwrap();
    let mut arr = [0u8; 33];
    arr.copy_from_slice(&public_key);
    assert_eq!(
        hex::encode(hash160(&arr)),
        "f54a5851e9372b87810a8e60cdd2e7cfd80b6e31"
    );
}
