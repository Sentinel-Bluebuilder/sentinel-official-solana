pub use solana_instruction_error::InstructionError;
use solana_sdk::{hash::Hash, instruction::Instruction, pubkey::Pubkey};
use solana_transaction_error::TransactionError;

#[track_caller]
pub fn assert_tx_within_limit(ixs: &[Instruction], payer: &Pubkey) {
    let msg = txv1::compile(payer, ixs, Hash::default(), &crate::send::HARNESS_TX_CONFIG)
        .expect("instructions should compile into a v1 message");
    let size = txv1::unsigned_wire_size(&msg);
    assert!(
        size <= txv1::MAX_TX_BYTES,
        "transaction exceeds the 4096-byte wire limit: {size} bytes"
    );
}

#[track_caller]
pub fn assert_distributor_error(
    err: &TransactionError,
    expected: distributor::error::DistributorError,
) {
    let expected_code = expected as u32 + anchor_lang::error::ERROR_CODE_OFFSET;
    match err {
        TransactionError::InstructionError(_, InstructionError::Custom(code))
            if *code == expected_code => {}
        other => panic!("expected Custom({expected_code}), got {other:?}"),
    }
}

#[track_caller]
pub fn assert_anchor_error(err: &TransactionError, expected: anchor_lang::error::ErrorCode) {
    let expected_code = expected as u32;
    match err {
        TransactionError::InstructionError(_, InstructionError::Custom(code))
            if *code == expected_code => {}
        other => panic!("expected Custom({expected_code}), got {other:?}"),
    }
}

#[track_caller]
pub fn assert_instruction_error(err: &TransactionError, expected: InstructionError) {
    match err {
        TransactionError::InstructionError(_, actual) if *actual == expected => {}
        other => panic!("expected {expected:?}, got {other:?}"),
    }
}
