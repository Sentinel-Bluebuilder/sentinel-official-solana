const HALF_ORDER: [u8; 32] = [
    0x7F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
    0x5D, 0x57, 0x6E, 0x73, 0x57, 0xA4, 0x50, 0x1D, 0xDF, 0xE9, 0x2F, 0x46, 0x68, 0x1B, 0x20, 0xA0,
];

#[must_use]
pub fn is_high_s(signature: &[u8; 64]) -> bool {
    signature[32..] > HALF_ORDER[..]
}

#[must_use]
pub fn compress_public_key(uncompressed: &[u8; 64]) -> [u8; 33] {
    let mut out = [0u8; 33];
    out[0] = if uncompressed[63] & 1 == 0 {
        0x02
    } else {
        0x03
    };
    out[1..33].copy_from_slice(&uncompressed[0..32]);
    out
}

#[cfg(target_os = "solana")]
#[must_use]
pub fn recover_compressed_public_key(
    message_hash: &[u8; 32],
    signature: &[u8; 64],
    recovery_id: u8,
) -> Option<[u8; 33]> {
    use solana_secp256k1_recover::secp256k1_recover;
    if recovery_id > 1 || is_high_s(signature) {
        return None;
    }
    let key = secp256k1_recover(message_hash, recovery_id, signature).ok()?;
    Some(compress_public_key(&key.to_bytes()))
}

#[cfg(not(target_os = "solana"))]
#[must_use]
pub fn recover_compressed_public_key(
    message_hash: &[u8; 32],
    signature: &[u8; 64],
    recovery_id: u8,
) -> Option<[u8; 33]> {
    use k256::ecdsa::{RecoveryId, Signature, VerifyingKey};
    if recovery_id > 1 || is_high_s(signature) {
        return None;
    }
    let parsed_signature = Signature::from_slice(signature).ok()?;
    let parsed_recovery_id = RecoveryId::from_byte(recovery_id)?;
    let verifying_key =
        VerifyingKey::recover_from_prehash(message_hash, &parsed_signature, parsed_recovery_id)
            .ok()?;
    let mut out = [0u8; 33];
    out.copy_from_slice(verifying_key.to_sec1_point(true).as_bytes());
    Some(out)
}
