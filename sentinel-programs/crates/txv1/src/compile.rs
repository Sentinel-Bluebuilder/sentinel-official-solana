use solana_hash::Hash;
use solana_instruction::Instruction;
use solana_keypair::Keypair;
use solana_message::{VersionedMessage, v1};
use solana_pubkey::Pubkey;
use solana_transaction::versioned::VersionedTransaction;

use crate::TxConfig;

#[derive(Debug, thiserror::Error)]
pub enum TxError {
    #[error("v1 compile failed: {0}")]
    CompileFailed(#[from] solana_message::CompileError),
    #[error("v1 message invalid: {0}")]
    InvalidTransaction(#[source] v1::MessageError),
    #[error("v1 sign failed: {0}")]
    SignFailed(#[from] solana_signer::SignerError),
    #[error("v1 serialize failed: {0}")]
    SerializeFailed(String),
}

pub fn compile(
    payer: &Pubkey,
    ixs: &[Instruction],
    blockhash: Hash,
    cfg: &TxConfig,
) -> Result<v1::Message, TxError> {
    let msg = v1::Message::try_compile_with_config(payer, ixs, blockhash, cfg.into())?;
    msg.validate().map_err(TxError::InvalidTransaction)?;
    Ok(msg)
}

pub fn sign(msg: v1::Message, signers: &[&Keypair]) -> Result<VersionedTransaction, TxError> {
    VersionedTransaction::try_new(VersionedMessage::V1(msg), signers).map_err(TxError::SignFailed)
}

pub fn build_signed(
    payer: &Pubkey,
    ixs: &[Instruction],
    signers: &[&Keypair],
    blockhash: Hash,
    cfg: &TxConfig,
) -> Result<VersionedTransaction, TxError> {
    sign(compile(payer, ixs, blockhash, cfg)?, signers)
}

#[cfg(test)]
mod tests {
    #![allow(
        clippy::unwrap_used,
        clippy::expect_used,
        clippy::panic,
        clippy::indexing_slicing,
        clippy::arithmetic_side_effects
    )]
    use super::*;
    use solana_signer::Signer;
    use solana_system_interface::instruction::transfer;

    #[test]
    fn compile_accepts_one_transfer() {
        let payer = Pubkey::new_unique();
        let instruction = transfer(&payer, &Pubkey::new_unique(), 1);
        let result = compile(
            &payer,
            &[instruction],
            Hash::default(),
            &TxConfig::new(1, 1),
        );
        assert!(result.is_ok(), "got {result:?}");
    }

    #[test]
    fn compile_rejects_over_64_addresses_via_validate() {
        let payer = Pubkey::new_unique();
        let ixs: Vec<_> = (0..64)
            .map(|_| transfer(&payer, &Pubkey::new_unique(), 1))
            .collect();
        let err = compile(&payer, &ixs, Hash::default(), &TxConfig::new(1, 1)).unwrap_err();
        assert!(matches!(err, TxError::InvalidTransaction(_)), "got {err:?}");
    }

    #[test]
    fn build_signed_yields_v1_versioned_transaction_with_one_signature() {
        let payer = Keypair::new();
        let instruction = transfer(&payer.pubkey(), &Pubkey::new_unique(), 1);
        let transaction = build_signed(
            &payer.pubkey(),
            &[instruction],
            &[&payer],
            Hash::default(),
            &TxConfig::new(1, 1),
        )
        .unwrap();
        assert!(
            matches!(transaction.message, VersionedMessage::V1(_)),
            "got {transaction:?}"
        );
        assert_eq!(transaction.signatures.len(), 1);
    }

    #[test]
    fn sign_rejects_non_signer_keypair() {
        let payer = Keypair::new();
        let other = Keypair::new();
        let instruction = transfer(&payer.pubkey(), &Pubkey::new_unique(), 1);
        let msg = compile(
            &payer.pubkey(),
            &[instruction],
            Hash::default(),
            &TxConfig::new(1, 1),
        )
        .unwrap();
        let err = sign(msg, &[&other]).unwrap_err();
        assert!(matches!(err, TxError::SignFailed(_)), "got {err:?}");
    }
}
