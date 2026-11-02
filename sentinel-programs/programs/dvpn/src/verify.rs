use anchor_lang::prelude::*;
use curve25519_dalek::scalar::Scalar;
use solana_curve25519::edwards::{PodEdwardsPoint, multiply_edwards, multiscalar_multiply_edwards};
use solana_curve25519::scalar::PodScalar;

use crate::error::DvpnError;

const ED25519_BASEPOINT: PodEdwardsPoint = PodEdwardsPoint([
    0x58, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66,
    0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66, 0x66,
]);

const EDWARDS_IDENTITY: [u8; 32] = {
    let mut bytes = [0u8; 32];
    bytes[0] = 1;
    bytes
};

fn sha512(parts: &[&[u8]]) -> [u8; 64] {
    solana_sha512_hasher::hashv(parts).to_bytes()
}

pub fn require_voucher_signature(
    public_key: &Pubkey,
    message: &[u8],
    signature: &[u8; 64],
) -> Result<()> {
    let a_bytes = public_key.to_bytes();
    let r_bytes: [u8; 32] = signature
        .get(0..32)
        .and_then(|bytes| bytes.try_into().ok())
        .ok_or_else(|| error!(DvpnError::InvalidVoucherSignature))?;
    let s_bytes: [u8; 32] = signature
        .get(32..64)
        .and_then(|bytes| bytes.try_into().ok())
        .ok_or_else(|| error!(DvpnError::InvalidVoucherSignature))?;

    let hash = sha512(&[&r_bytes, &a_bytes, message]);
    #[allow(clippy::arithmetic_side_effects)]
    let neg_challenge = (-Scalar::from_bytes_mod_order_wide(&hash)).to_bytes();

    let r_prime = multiscalar_multiply_edwards(
        &[PodScalar(s_bytes), PodScalar(neg_challenge)],
        &[ED25519_BASEPOINT, PodEdwardsPoint(a_bytes)],
    )
    .ok_or_else(|| error!(DvpnError::InvalidVoucherSignature))?;

    require!(r_prime.0 == r_bytes, DvpnError::InvalidVoucherSignature);
    Ok(())
}

pub fn require_voucher_signer_prime_order(public_key: &Pubkey) -> Result<()> {
    let cofactor = {
        let mut bytes = [0u8; 32];
        bytes[0] = 8;
        PodScalar(bytes)
    };
    match multiply_edwards(&cofactor, &PodEdwardsPoint(public_key.to_bytes())) {
        Some(point) if point.0 != EDWARDS_IDENTITY => Ok(()),
        _ => Err(error!(DvpnError::InvalidVoucherSigner)),
    }
}
