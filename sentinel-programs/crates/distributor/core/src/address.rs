use crate::consts::HRP;
use bech32::{Bech32, Hrp};

#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
#[non_exhaustive]
pub enum AddressError {
    #[error("malformed, Bech32m, or non-canonical bech32 address")]
    DecodeFailed,
    #[error("wrong bech32 HRP (expected `sent`)")]
    HrpMismatch,
    #[error("bech32 data part is not 20 bytes")]
    LengthMismatch,
    #[error("address contains a JSON-special character")]
    NotJsonSafe,
}

#[must_use]
pub fn encode_bech32_address(address_bytes: &[u8; 20]) -> String {
    #[allow(clippy::expect_used)]
    {
        let hrp = Hrp::parse(HRP).expect("HRP is a valid compile-time constant");
        bech32::encode::<Bech32>(hrp, address_bytes).expect("20-byte payload always encodes")
    }
}

pub fn decode_bech32_address(text: &str) -> Result<[u8; 20], AddressError> {
    let (hrp, data) = bech32::decode(text).map_err(|_| AddressError::DecodeFailed)?;
    if hrp.as_str() != HRP {
        return Err(AddressError::HrpMismatch);
    }
    let address_bytes: [u8; 20] = data.try_into().map_err(|_| AddressError::LengthMismatch)?;
    if encode_bech32_address(&address_bytes) != text {
        return Err(AddressError::DecodeFailed);
    }
    Ok(address_bytes)
}
