use distributor_adr036::identity::{is_address_of_public_key, sentinel_address};
use distributor_adr036::payload::claim_payload;
use distributor_adr036::recover::{is_high_s, recover_compressed_public_key};
use distributor_adr036::sign_doc::sign_doc_hash;
use distributor_core::{Bech32Address, Beneficiary, ProgramId, RootsCommitment};
use k256::ecdsa::SigningKey;
use solana_pubkey::Pubkey;

fn test_signer() -> Bech32Address {
    Bech32Address::parse("sent1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqgckxrj").unwrap()
}

fn compressed_public_key_of(signing_key: &SigningKey) -> [u8; 33] {
    let point = signing_key.verifying_key().to_sec1_point(true);
    let mut out = [0u8; 33];
    out.copy_from_slice(point.as_bytes());
    out
}

const CURVE_ORDER_MINUS_ONE: [u8; 32] = [
    0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFE,
    0xBA, 0xAE, 0xDC, 0xE6, 0xAF, 0x48, 0xA0, 0x3B, 0xBF, 0xD2, 0x5E, 0x8C, 0xD0, 0x36, 0x41, 0x40,
];

#[test]
fn recover_roundtrips_and_matches_identity() {
    let signing_key = SigningKey::from_slice(&[7u8; 32]).unwrap();
    let compressed = compressed_public_key_of(&signing_key);
    let signer = sentinel_address(&compressed);
    let signer_address = Bech32Address::parse(&signer).unwrap();
    let payload = claim_payload(
        &Beneficiary::from_public_key(Pubkey::new_from_array([1u8; 32])),
        &RootsCommitment::from_bytes([2u8; 32]),
        &ProgramId::from_public_key(Pubkey::new_from_array([3u8; 32])),
    );
    let hash = sign_doc_hash(&signer_address, &payload);
    let (recoverable_signature, recovery_id) = signing_key.sign_prehash_recoverable(&hash);
    let signature: [u8; 64] = recoverable_signature.to_bytes().into();
    let recovered =
        recover_compressed_public_key(&hash, &signature, recovery_id.to_byte()).unwrap();
    assert_eq!(recovered, compressed);
    assert!(is_address_of_public_key(&recovered, &signer));
}

#[test]
fn wrong_recovery_id_yields_a_different_key() {
    let signing_key = SigningKey::from_slice(&[9u8; 32]).unwrap();
    let compressed = compressed_public_key_of(&signing_key);
    let hash = sign_doc_hash(&test_signer(), b"x");
    let (recoverable_signature, recovery_id) = signing_key.sign_prehash_recoverable(&hash);
    let signature: [u8; 64] = recoverable_signature.to_bytes().into();
    if let Some(recovered) =
        recover_compressed_public_key(&hash, &signature, recovery_id.to_byte() ^ 1)
    {
        assert_ne!(
            recovered, compressed,
            "wrong id must not recover the signer key"
        );
    }
}

#[test]
fn high_s_signature_is_rejected() {
    let signing_key = SigningKey::from_slice(&[5u8; 32]).unwrap();
    let hash = sign_doc_hash(&test_signer(), b"x");
    let (recoverable_signature, recovery_id) = signing_key.sign_prehash_recoverable(&hash);
    let mut signature: [u8; 64] = recoverable_signature.to_bytes().into();
    signature[32..64].copy_from_slice(&CURVE_ORDER_MINUS_ONE);
    assert!(is_high_s(&signature));
    assert!(recover_compressed_public_key(&hash, &signature, recovery_id.to_byte()).is_none());
}

#[test]
fn recovery_id_above_one_is_rejected() {
    let signing_key = SigningKey::from_slice(&[3u8; 32]).unwrap();
    let hash = sign_doc_hash(&test_signer(), b"x");
    let (recoverable_signature, _rid) = signing_key.sign_prehash_recoverable(&hash);
    let signature: [u8; 64] = recoverable_signature.to_bytes().into();
    assert!(recover_compressed_public_key(&hash, &signature, 2).is_none());
    assert!(recover_compressed_public_key(&hash, &signature, 3).is_none());
}

#[test]
fn syscall_path_and_host_path_agree() {
    use distributor_adr036::recover::compress_public_key;
    for seed in [13u8, 21, 99] {
        let signing_key = SigningKey::from_slice(&[seed; 32]).unwrap();
        let hash = sign_doc_hash(&test_signer(), b"parity");
        let (recoverable_signature, recovery_id) = signing_key.sign_prehash_recoverable(&hash);
        let signature: [u8; 64] = recoverable_signature.to_bytes().into();
        let uncompressed =
            solana_secp256k1_recover::secp256k1_recover(&hash, recovery_id.to_byte(), &signature)
                .expect("well-formed signature recovers")
                .to_bytes();
        assert_eq!(
            compress_public_key(&uncompressed),
            recover_compressed_public_key(&hash, &signature, recovery_id.to_byte()).unwrap(),
            "seed={seed}"
        );
    }
}
