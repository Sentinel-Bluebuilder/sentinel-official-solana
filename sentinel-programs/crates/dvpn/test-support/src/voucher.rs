use dvpn_core::voucher::{VOUCHER_MESSAGE_BYTES, channel_voucher_message, lease_voucher_message};
use ed25519_dalek::Signer;
pub use ed25519_dalek::SigningKey;
use solana_sdk::{pubkey::Pubkey, signature::Keypair};

pub fn voucher_signing_key(seed: u8) -> SigningKey {
    SigningKey::from_bytes(&[seed; 32])
}

fn sign_message(key: &SigningKey, message: &[u8; VOUCHER_MESSAGE_BYTES]) -> [u8; 64] {
    key.sign(message).to_bytes()
}

pub fn sign_channel_voucher(
    key: &SigningKey,
    channel: &Pubkey,
    opened_at: i64,
    cumulative_amount: u64,
) -> [u8; 64] {
    sign_message(
        key,
        &channel_voucher_message(channel, opened_at, cumulative_amount),
    )
}

pub fn sign_lease_voucher(
    key: &SigningKey,
    lease: &Pubkey,
    opened_at: i64,
    cumulative_amount: u64,
) -> [u8; 64] {
    sign_message(
        key,
        &lease_voucher_message(lease, opened_at, cumulative_amount),
    )
}

#[must_use]
pub fn sign_channel_voucher_with_wallet(
    wallet: &Keypair,
    channel: &Pubkey,
    opened_at: i64,
    cumulative_amount: u64,
) -> [u8; 64] {
    let key = SigningKey::from_bytes(wallet.secret_bytes());
    sign_channel_voucher(&key, channel, opened_at, cumulative_amount)
}

#[must_use]
pub fn sign_lease_voucher_with_wallet(
    wallet: &Keypair,
    lease: &Pubkey,
    opened_at: i64,
    cumulative_amount: u64,
) -> [u8; 64] {
    let key = SigningKey::from_bytes(wallet.secret_bytes());
    sign_lease_voucher(&key, lease, opened_at, cumulative_amount)
}
