use base64::{Engine as _, engine::general_purpose::STANDARD};
use distributor_adr036::payload::claim_payload;
use distributor_adr036::sign_doc::build_sign_doc;
use distributor_core::hash::hex_lower;
use distributor_core::{Bech32Address, Beneficiary, ProgramId, RootsCommitment};

fn beneficiary() -> Beneficiary {
    Beneficiary::from_public_key(solana_pubkey::Pubkey::new_from_array([0x11u8; 32]))
}

fn program_id() -> ProgramId {
    ProgramId::from_public_key(solana_pubkey::Pubkey::new_from_array([0x22u8; 32]))
}

fn roots() -> RootsCommitment {
    RootsCommitment::from_bytes([0x33u8; 32])
}

#[test]
fn claim_payload_is_pinned_format() {
    let payload = claim_payload(&beneficiary(), &roots(), &program_id());
    let want = format!(
        "sentinel-claim:{}:{}:{}",
        bs58::encode(beneficiary().to_bytes()).into_string(),
        hex_lower(roots().as_bytes()),
        bs58::encode(program_id().to_bytes()).into_string(),
    );
    assert_eq!(payload, want.into_bytes());
}

fn test_signer() -> Bech32Address {
    Bech32Address::parse("sent1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqgckxrj").unwrap()
}

#[test]
fn sign_doc_cannot_be_built_from_an_unvalidated_string() {
    assert!(matches!(
        Bech32Address::parse("sent1\"evil"),
        Err(distributor_core::address::AddressError::NotJsonSafe)
    ));
}

#[test]
fn sign_doc_is_compact_sorted_amino_json() {
    let signer = test_signer();
    let payload = b"hello";
    let data_b64 = STANDARD.encode(payload);
    let signer_str = signer.as_str();
    let want = format!(
        "{{\"account_number\":\"0\",\"chain_id\":\"\",\"fee\":{{\"amount\":[],\"gas\":\"0\"}},\"memo\":\"\",\"msgs\":[{{\"type\":\"sign/MsgSignData\",\"value\":{{\"data\":\"{data_b64}\",\"signer\":\"{signer_str}\"}}}}],\"sequence\":\"0\"}}"
    );
    assert_eq!(build_sign_doc(&signer, payload), want.into_bytes());
}
