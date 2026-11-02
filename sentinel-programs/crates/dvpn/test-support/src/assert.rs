use dvpn::error::DvpnError;
use litesvm::types::FailedTransactionMetadata;
use solana_sdk::instruction::InstructionError;
#[allow(deprecated)]
use solana_sdk::transaction::TransactionError;

fn expected_custom_code(error: DvpnError) -> u32 {
    u32::from(error)
}

#[track_caller]
pub fn assert_dvpn_error(failed: &FailedTransactionMetadata, expected: DvpnError) {
    let expected_code = expected_custom_code(expected);
    match &failed.err {
        TransactionError::InstructionError(_, InstructionError::Custom(code))
            if *code == expected_code => {}
        other => panic!(
            "expected Custom({expected_code}), got {other:?}\n{}",
            failed.meta.pretty_logs()
        ),
    }
}

#[track_caller]
pub fn assert_anchor_error(
    failed: &FailedTransactionMetadata,
    expected: anchor_lang::error::ErrorCode,
) {
    let code = expected as u32;
    match &failed.err {
        TransactionError::InstructionError(_, InstructionError::Custom(code_actual))
            if *code_actual == code => {}
        other => panic!(
            "expected Custom({code}), got {other:?}\n{}",
            failed.meta.pretty_logs()
        ),
    }
}
