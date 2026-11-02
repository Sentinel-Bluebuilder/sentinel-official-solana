use litesvm::{LiteSVM, types::TransactionMetadata};
use solana_sdk::{instruction::Instruction, pubkey::Pubkey, signature::Keypair, signer::Signer};
use solana_transaction_error::TransactionError;
use txv1::TxConfig;

pub const HARNESS_TX_CONFIG: TxConfig = TxConfig::new(
    distributor_core::consts::CLAIM_COMPUTE_UNIT_LIMIT,
    64 * 1024 * 1024,
);

#[track_caller]
pub fn send(svm: &mut LiteSVM, payer: &Keypair, instruction: Instruction) -> TransactionMetadata {
    let transaction = txv1::build_signed(
        &payer.pubkey(),
        &[instruction],
        &[payer],
        svm.latest_blockhash(),
        &HARNESS_TX_CONFIG,
    )
    .expect("harness transaction should compile and sign");
    svm.send_transaction(transaction).unwrap_or_else(|failed| {
        panic!(
            "expected success, got {:?}\n{}",
            failed.err,
            failed.meta.pretty_logs()
        )
    })
}

#[track_caller]
pub fn send_expect_err(
    svm: &mut LiteSVM,
    payer: &Keypair,
    instruction: Instruction,
) -> TransactionError {
    let transaction = txv1::build_signed(
        &payer.pubkey(),
        &[instruction],
        &[payer],
        svm.latest_blockhash(),
        &HARNESS_TX_CONFIG,
    )
    .expect("harness transaction should compile and sign");
    svm.send_transaction(transaction)
        .expect_err("expected transaction to fail")
        .err
}

#[track_caller]
pub fn resend_expect_err(
    svm: &mut LiteSVM,
    payer: &Keypair,
    instruction: Instruction,
) -> TransactionError {
    svm.expire_blockhash();
    send_expect_err(svm, payer, instruction)
}

pub fn transfer_lamports(svm: &mut LiteSVM, from: &Keypair, recipient: &Pubkey, lamports: u64) {
    send(
        svm,
        from,
        solana_system_interface::instruction::transfer(&from.pubkey(), recipient, lamports),
    );
}
