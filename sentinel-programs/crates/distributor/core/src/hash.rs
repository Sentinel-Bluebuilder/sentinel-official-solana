#[must_use]
pub fn keccak256(parts: &[&[u8]]) -> [u8; 32] {
    solana_keccak_hasher::hashv(parts).to_bytes()
}

#[must_use]
pub fn hex_lower(bytes: &[u8]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut out = String::with_capacity(bytes.len().saturating_mul(2));
    for &byte in bytes {
        #[allow(clippy::indexing_slicing)]
        let (high, low) = (HEX[usize::from(byte >> 4)], HEX[usize::from(byte & 0x0f)]);
        out.push(char::from(high));
        out.push(char::from(low));
    }
    out
}

#[must_use]
pub fn hash160(compressed_public_key: &[u8; 33]) -> [u8; 20] {
    use ripemd::Ripemd160;
    use sha2::{Digest, Sha256};
    let sha = Sha256::digest(compressed_public_key);
    let rip = Ripemd160::digest(sha);
    let mut out = [0u8; 20];
    out.copy_from_slice(&rip);
    out
}
