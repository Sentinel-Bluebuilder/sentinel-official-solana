use xtask::ledger::{LedgerError, ParsedRecord, decode_ledger, encode_ledger};

fn fixed_record(address_byte: u8, liquid: u64, locked: u64) -> ParsedRecord {
    ParsedRecord {
        bech32_address: distributor_core::address::encode_bech32_address(&[address_byte; 20]),
        compressed_public_key: None,
        liquid_amount: liquid,
        locked_amount: locked,
        schedule: None,
    }
}

#[test]
fn encode_ledger_output_is_byte_pinned() {
    let recs = vec![
        fixed_record(0x03, 300, 0),
        fixed_record(0x01, 100, 0),
        fixed_record(0x02, 200, 0),
    ];
    let bytes = encode_ledger(&recs);
    assert_eq!(
        hex::encode(&bytes),
        "53454e544c4544470100000003000000000000002b00000073656e7431717971737a716770717971737a716770717971737a716770717971737a71677066676838666e0064000000000000000000000000000000002b00000073656e7431716770717971737a716770717971737a716770717971737a716770717971737a6376337a7a3900c8000000000000000000000000000000002b00000073656e74317176707378716372717670737871637271767073787163727176707378716372657573726779002c0100000000000000000000000000000008e1c0a28288688fa110f62be84a50361909f73a09d1b414fb7bc64bd65274a5"
    );
}

fn ledger_with_raw_address(address: &str, liquid: u64) -> Vec<u8> {
    let mut body = Vec::new();
    body.extend_from_slice(b"SENTLEDG");
    body.extend_from_slice(&1u32.to_le_bytes());
    body.extend_from_slice(&1u64.to_le_bytes());
    let address_bytes = address.as_bytes();
    body.extend_from_slice(&u32::try_from(address_bytes.len()).unwrap().to_le_bytes());
    body.extend_from_slice(address_bytes);
    body.push(0);
    body.extend_from_slice(&liquid.to_le_bytes());
    body.extend_from_slice(&0u64.to_le_bytes());
    body.push(0);
    let hash = distributor_core::hash::keccak256(&[&body]);
    body.extend_from_slice(&hash);
    body
}

#[test]
fn decode_ledger_rejects_a_non_canonical_bech32_address() {
    let canon = distributor_core::address::encode_bech32_address(&[7u8; 20]);
    let non_canonical = canon.to_uppercase();
    let bytes = ledger_with_raw_address(&non_canonical, 100);
    assert!(
        matches!(decode_ledger(&bytes), Err(LedgerError::InvalidAddress)),
        "decode_bech32_address's own canonical-form rejection must still surface \
         through decode_ledger as the only canonical-form check"
    );
}
