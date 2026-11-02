use base64::{Engine as _, engine::general_purpose::STANDARD};
use distributor_core::Bech32Address;
use sha2::{Digest, Sha256};

#[must_use]
pub fn build_sign_doc(signer: &Bech32Address, data_payload: &[u8]) -> Vec<u8> {
    let signer = signer.as_str();
    let data_b64 = STANDARD.encode(data_payload);
    format!(
        "{{\"account_number\":\"0\",\"chain_id\":\"\",\"fee\":{{\"amount\":[],\"gas\":\"0\"}},\"memo\":\"\",\"msgs\":[{{\"type\":\"sign/MsgSignData\",\"value\":{{\"data\":\"{data_b64}\",\"signer\":\"{signer}\"}}}}],\"sequence\":\"0\"}}"
    )
    .into_bytes()
}

#[must_use]
pub fn sign_doc_hash(signer: &Bech32Address, data_payload: &[u8]) -> [u8; 32] {
    Sha256::digest(build_sign_doc(signer, data_payload)).into()
}
