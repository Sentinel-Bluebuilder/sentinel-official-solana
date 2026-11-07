use anchor_lang::prelude::*;

/// sha256 of `sentinel-dvpn/channel-voucher/v1`.
pub const CHANNEL_VOUCHER_DOMAIN: [u8; 32] = [
    0x0a, 0x34, 0xaa, 0x1e, 0x23, 0x30, 0x37, 0xf6, 0x55, 0x8a, 0x36, 0x4b, 0xaf, 0x39, 0xc4, 0x30,
    0x23, 0x6b, 0xdf, 0xc9, 0x99, 0x43, 0xbd, 0xee, 0xc1, 0xba, 0xe0, 0xee, 0x72, 0x22, 0xb3, 0x54,
];

/// sha256 of `sentinel-dvpn/lease-voucher/v1`.
pub const LEASE_VOUCHER_DOMAIN: [u8; 32] = [
    0x98, 0xa6, 0x6b, 0x3e, 0x1f, 0x23, 0x7c, 0x2e, 0x53, 0x8a, 0x89, 0xbf, 0xf2, 0x83, 0x3c, 0x60,
    0x4b, 0x34, 0x0b, 0x70, 0x02, 0x3e, 0xc1, 0x40, 0x36, 0x59, 0xae, 0x9e, 0xb5, 0x59, 0xd1, 0xc3,
];

pub const VOUCHER_MESSAGE_BYTES: usize = 80;

fn voucher_message(
    domain: &[u8; 32],
    position_address: &Pubkey,
    opened_at: i64,
    cumulative_amount: u64,
) -> [u8; VOUCHER_MESSAGE_BYTES] {
    let mut message = [0u8; VOUCHER_MESSAGE_BYTES];
    let (domain_part, rest) = message.split_at_mut(32);
    domain_part.copy_from_slice(domain);
    let (position_part, rest) = rest.split_at_mut(32);
    position_part.copy_from_slice(&position_address.to_bytes());
    let (opened_part, cumulative_part) = rest.split_at_mut(8);
    opened_part.copy_from_slice(&opened_at.to_le_bytes());
    cumulative_part.copy_from_slice(&cumulative_amount.to_le_bytes());
    message
}

#[must_use]
pub fn channel_voucher_message(
    channel_pda: &Pubkey,
    opened_at: i64,
    cumulative_amount: u64,
) -> [u8; VOUCHER_MESSAGE_BYTES] {
    voucher_message(
        &CHANNEL_VOUCHER_DOMAIN,
        channel_pda,
        opened_at,
        cumulative_amount,
    )
}

#[must_use]
pub fn lease_voucher_message(
    lease_pda: &Pubkey,
    opened_at: i64,
    cumulative_amount: u64,
) -> [u8; VOUCHER_MESSAGE_BYTES] {
    voucher_message(
        &LEASE_VOUCHER_DOMAIN,
        lease_pda,
        opened_at,
        cumulative_amount,
    )
}

#[derive(AnchorSerialize, AnchorDeserialize, Clone, PartialEq, Eq, Debug)]
pub struct Voucher {
    pub cumulative_amount: u64,
    pub signature: [u8; 64],
}

/// A voucher redeemed for less than it was signed for. The signature over `cumulative_amount`
/// authorizes the node to take up to that total; `amount` is the total it claims, so the rest
/// of the escrow stays with the client and is returned at close.
#[derive(AnchorSerialize, AnchorDeserialize, Clone, PartialEq, Eq, Debug)]
pub struct PartialVoucher {
    pub cumulative_amount: u64,
    pub signature: [u8; 64],
    pub amount: u64,
}

impl From<Voucher> for PartialVoucher {
    /// A whole voucher is a partial voucher that claims everything it was signed for.
    fn from(voucher: Voucher) -> Self {
        Self {
            cumulative_amount: voucher.cumulative_amount,
            signature: voucher.signature,
            amount: voucher.cumulative_amount,
        }
    }
}

#[cfg(test)]
mod tests {
    #![allow(clippy::unwrap_used, clippy::indexing_slicing, clippy::panic)]
    use super::*;

    #[test]
    fn channel_message_layout() {
        let position_address = Pubkey::new_from_array([9u8; 32]);
        let message = channel_voucher_message(
            &position_address,
            0x0A0B_0C0D_0E0F_1011,
            0x0102_0304_0506_0708,
        );
        assert_eq!(message.len(), VOUCHER_MESSAGE_BYTES);
        assert_eq!(VOUCHER_MESSAGE_BYTES, 80);
        assert_eq!(&message[..32], &CHANNEL_VOUCHER_DOMAIN);
        assert_eq!(&message[32..64], &position_address.to_bytes());
        assert_eq!(&message[64..72], &0x0A0B_0C0D_0E0F_1011i64.to_le_bytes());
        assert_eq!(&message[72..80], &0x0102_0304_0506_0708u64.to_le_bytes());
    }

    #[test]
    fn lease_message_uses_lease_domain_and_same_layout() {
        let position_address = Pubkey::new_from_array([7u8; 32]);
        let message = lease_voucher_message(&position_address, -5, 42);
        assert_eq!(&message[..32], &LEASE_VOUCHER_DOMAIN);
        assert_eq!(&message[32..64], &position_address.to_bytes());
        assert_eq!(&message[64..72], &(-5i64).to_le_bytes());
        assert_eq!(&message[72..80], &42u64.to_le_bytes());
    }

    #[test]
    fn different_opened_at_gives_different_message() {
        let position_address = Pubkey::new_from_array([1u8; 32]);
        assert_ne!(
            channel_voucher_message(&position_address, 1, 100),
            channel_voucher_message(&position_address, 2, 100)
        );
    }
}
