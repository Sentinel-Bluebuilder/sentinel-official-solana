use distributor_core::hash::hex_lower;
use distributor_core::{Beneficiary, ProgramId, RootsCommitment};
use solana_pubkey::Pubkey;

#[must_use]
pub fn claim_payload(
    beneficiary: &Beneficiary,
    roots_commitment: &RootsCommitment,
    program_id: &ProgramId,
) -> Vec<u8> {
    let mut payload = Vec::new();
    payload.extend_from_slice(b"sentinel-claim:");
    push_base58(&mut payload, beneficiary.as_public_key());
    payload.push(b':');
    payload.extend_from_slice(hex_lower(roots_commitment.as_bytes()).as_bytes());
    payload.push(b':');
    push_base58(&mut payload, program_id.as_public_key());
    payload
}

#[allow(clippy::expect_used)]
fn push_base58(out: &mut Vec<u8>, key: &Pubkey) {
    bs58::encode(key.to_bytes())
        .onto(out)
        .expect("Vec<u8> target never returns BufferTooSmall");
}
