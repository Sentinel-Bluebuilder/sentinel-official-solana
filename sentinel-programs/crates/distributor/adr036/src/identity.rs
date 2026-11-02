use distributor_core::address::encode_bech32_address;
use distributor_core::hash::hash160;

#[must_use]
pub fn sentinel_address(compressed_public_key: &[u8; 33]) -> String {
    encode_bech32_address(&hash160(compressed_public_key))
}

#[must_use]
pub fn is_address_of_public_key(compressed_public_key: &[u8; 33], bech32_address: &str) -> bool {
    sentinel_address(compressed_public_key) == bech32_address
}

#[cfg(test)]
mod tests {
    use super::*;

    const COMPRESSED_PUBLIC_KEY: [u8; 33] = [
        0x02, 0x79, 0xBE, 0x66, 0x7E, 0xF9, 0xDC, 0xBB, 0xAC, 0x55, 0xA0, 0x62, 0x95, 0xCE, 0x87,
        0x0B, 0x07, 0x02, 0x9B, 0xFC, 0xDB, 0x2D, 0xCE, 0x28, 0xD9, 0x59, 0xF2, 0x81, 0x5B, 0x16,
        0xF8, 0x17, 0x98,
    ];

    #[test]
    fn is_address_of_public_key_matches_canonical_lowercase() {
        let lower = sentinel_address(&COMPRESSED_PUBLIC_KEY);
        assert!(is_address_of_public_key(&COMPRESSED_PUBLIC_KEY, &lower));
    }

    #[test]
    fn is_address_of_public_key_rejects_mixed_case() {
        let lower = sentinel_address(&COMPRESSED_PUBLIC_KEY);
        let upper = lower.to_uppercase();
        assert_ne!(lower, upper, "fixture must actually contain letters");
        assert!(!is_address_of_public_key(&COMPRESSED_PUBLIC_KEY, &upper));
    }

    #[test]
    fn is_address_of_public_key_rejects_wrong_address() {
        assert!(!is_address_of_public_key(
            &COMPRESSED_PUBLIC_KEY,
            "sent1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"
        ));
    }
}
