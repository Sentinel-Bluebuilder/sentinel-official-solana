use distributor_core::address::{AddressError, decode_bech32_address, encode_bech32_address};

#[test]
fn bech32_round_trips_lowercase_with_sent_hrp() {
    let data = [0x11u8; 20];
    let address = encode_bech32_address(&data);
    assert!(address.starts_with("sent1"), "got {address}");
    assert_eq!(
        address,
        address.to_lowercase(),
        "encoder must emit canonical lowercase"
    );
    assert_eq!(decode_bech32_address(&address).unwrap(), data);
}

#[test]
fn bech32_rejects_wrong_hrp_wrong_len_and_malformed() {
    let other =
        bech32::encode::<bech32::Bech32>(bech32::Hrp::parse("cosmos").unwrap(), &[0x11u8; 20])
            .unwrap();
    assert!(matches!(
        decode_bech32_address(&other),
        Err(AddressError::HrpMismatch)
    ));
    let long = bech32::encode::<bech32::Bech32>(bech32::Hrp::parse("sent").unwrap(), &[0x11u8; 21])
        .unwrap();
    assert!(matches!(
        decode_bech32_address(&long),
        Err(AddressError::LengthMismatch)
    ));
    assert!(matches!(
        decode_bech32_address("not-a-bech32-string"),
        Err(AddressError::DecodeFailed)
    ));
}

#[test]
fn bech32m_checksum_is_rejected() {
    let encoded =
        bech32::encode::<bech32::Bech32m>(bech32::Hrp::parse("sent").unwrap(), &[0x11u8; 20])
            .unwrap();
    assert!(matches!(
        decode_bech32_address(&encoded),
        Err(AddressError::DecodeFailed)
    ));
}

#[test]
fn bech32_rejects_uppercase() {
    let lower = encode_bech32_address(&[0x11u8; 20]);
    let upper = lower.to_uppercase();
    assert_ne!(
        lower, upper,
        "fixture must contain letters for this to test anything"
    );
    assert!(matches!(
        decode_bech32_address(&upper),
        Err(AddressError::HrpMismatch)
    ));
}
