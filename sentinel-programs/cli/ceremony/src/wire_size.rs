use crate::error::CeremonyError;
use solana_hash::Hash;
use solana_instruction::Instruction;
use solana_pubkey::Pubkey;
use txv1::{MAX_TX_BYTES, TxConfig, compile, unsigned_wire_size};

#[must_use]
pub fn tx_wire_bytes(ixs: &[Instruction], payer: &Pubkey, tx_config: &TxConfig) -> usize {
    #[allow(clippy::expect_used)]
    let message = compile(payer, ixs, Hash::default(), tx_config)
        .expect("ceremony instructions always compile to a valid v1 message");
    unsigned_wire_size(&message)
}

pub fn check_tx_within_limit(
    ixs: &[Instruction],
    payer: &Pubkey,
    tx_config: &TxConfig,
) -> Result<(), CeremonyError> {
    let size_bytes = tx_wire_bytes(ixs, payer, tx_config);
    if size_bytes <= MAX_TX_BYTES {
        Ok(())
    } else {
        Err(CeremonyError::WireLimitExceeded {
            limit_bytes: MAX_TX_BYTES,
            size_bytes,
        })
    }
}
