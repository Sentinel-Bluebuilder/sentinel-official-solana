use solana_message::v1;
use solana_transaction::versioned::VersionedTransaction;

use crate::TxError;

#[derive(Debug, thiserror::Error)]
#[error("transaction is {size_bytes} bytes, over the {limit_bytes}-byte v1 limit")]
pub struct SizeError {
    pub size_bytes: usize,
    pub limit_bytes: usize,
}

pub fn wire_size(transaction: &VersionedTransaction) -> Result<usize, TxError> {
    let size = wincode::serialized_size(transaction)
        .map_err(|error| TxError::SerializeFailed(error.to_string()))?;
    usize::try_from(size).map_err(|error| TxError::SerializeFailed(error.to_string()))
}

#[must_use]
pub fn unsigned_wire_size(msg: &v1::Message) -> usize {
    1usize
        .saturating_add(msg.size())
        .saturating_add(64usize.saturating_mul(usize::from(msg.header.num_required_signatures)))
}

pub fn check_within_limit(size_bytes: usize) -> Result<(), SizeError> {
    if size_bytes <= crate::MAX_TX_BYTES {
        Ok(())
    } else {
        Err(SizeError {
            size_bytes,
            limit_bytes: crate::MAX_TX_BYTES,
        })
    }
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
    use crate::{TxConfig, build_signed};
    use solana_hash::Hash;
    use solana_keypair::Keypair;
    use solana_message::VersionedMessage;
    use solana_pubkey::Pubkey;
    use solana_signer::Signer;
    use solana_system_interface::instruction::transfer;

    #[test]
    fn wire_size_matches_version_byte_plus_message_plus_signatures() {
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
        let VersionedMessage::V1(msg) = &transaction.message else {
            panic!("expected v1 message, got {:?}", transaction.message);
        };
        let expected = 1 + msg.size() + 64 * usize::from(msg.header.num_required_signatures);
        assert_eq!(wire_size(&transaction).unwrap(), expected);
        assert_eq!(unsigned_wire_size(msg), expected);
    }

    #[test]
    fn check_within_limit_rejects_4097() {
        let err = check_within_limit(4097).unwrap_err();
        assert_eq!(err.size_bytes, 4097);
        assert_eq!(err.limit_bytes, 4096);
    }
}
