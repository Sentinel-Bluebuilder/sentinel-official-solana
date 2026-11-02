use distributor_core::address::{AddressError, encode_bech32_address};
use distributor_core::types::Bech32Address;
use distributor_core::types::{Beneficiary, ProgramId};
use solana_pubkey::Pubkey;

#[test]
fn bech32_address_rejects_json_special_characters() {
    assert!(matches!(
        Bech32Address::parse("sent1\"injected"),
        Err(AddressError::NotJsonSafe)
    ));
    assert!(matches!(
        Bech32Address::parse("sent1\\injected"),
        Err(AddressError::NotJsonSafe)
    ));
}

#[test]
fn bech32_address_rejects_control_characters() {
    assert!(matches!(
        Bech32Address::parse("sent1\ninjected"),
        Err(AddressError::NotJsonSafe)
    ));
    assert!(matches!(
        Bech32Address::parse("sent1\tinjected"),
        Err(AddressError::NotJsonSafe)
    ));
    assert!(matches!(
        Bech32Address::parse("sent1\u{0}injected"),
        Err(AddressError::NotJsonSafe)
    ));
}

#[test]
fn bech32_address_accepts_a_canonical_address() {
    let address = encode_bech32_address(&[0u8; 20]);
    let parsed = Bech32Address::parse(&address).expect("canonical address must parse");
    assert_eq!(parsed.as_str(), address);
}

#[test]
fn program_id_and_beneficiary_do_not_interconvert() {
    let program_id = ProgramId::from_public_key(Pubkey::new_from_array([1u8; 32]));
    let beneficiary = Beneficiary::from_public_key(Pubkey::new_from_array([2u8; 32]));
    assert_eq!(program_id.to_bytes(), [1u8; 32]);
    assert_eq!(beneficiary.to_bytes(), [2u8; 32]);
    assert_ne!(program_id.as_public_key(), beneficiary.as_public_key());
}
