use crate::address::{AddressError, decode_bech32_address};
use solana_pubkey::Pubkey;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub struct RootsCommitment([u8; 32]);

impl RootsCommitment {
    #[must_use]
    pub const fn from_bytes(bytes: [u8; 32]) -> Self {
        Self(bytes)
    }

    #[must_use]
    pub const fn as_bytes(&self) -> &[u8; 32] {
        &self.0
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Hash)]
pub struct Bech32Address {
    text: String,
    address_bytes: [u8; 20],
}

impl Bech32Address {
    pub fn parse(text: &str) -> Result<Self, AddressError> {
        if text
            .bytes()
            .any(|byte| byte == b'"' || byte == b'\\' || byte < 0x20)
        {
            return Err(AddressError::NotJsonSafe);
        }
        let address_bytes = decode_bech32_address(text)?;
        Ok(Self {
            text: text.to_owned(),
            address_bytes,
        })
    }

    #[must_use]
    pub fn as_str(&self) -> &str {
        &self.text
    }

    #[must_use]
    pub const fn address_bytes(&self) -> &[u8; 20] {
        &self.address_bytes
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub struct ProgramId(Pubkey);

impl ProgramId {
    #[must_use]
    pub const fn from_public_key(public_key: Pubkey) -> Self {
        Self(public_key)
    }

    #[must_use]
    pub const fn as_public_key(&self) -> &Pubkey {
        &self.0
    }

    #[must_use]
    pub fn to_bytes(&self) -> [u8; 32] {
        self.0.to_bytes()
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub struct Beneficiary(Pubkey);

impl Beneficiary {
    #[must_use]
    pub const fn from_public_key(public_key: Pubkey) -> Self {
        Self(public_key)
    }

    #[must_use]
    pub const fn as_public_key(&self) -> &Pubkey {
        &self.0
    }

    #[must_use]
    pub fn to_bytes(&self) -> [u8; 32] {
        self.0.to_bytes()
    }
}
