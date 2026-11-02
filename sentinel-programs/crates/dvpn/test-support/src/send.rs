use litesvm::LiteSVM;
use litesvm::types::{FailedTransactionMetadata, TransactionMetadata};
#[allow(deprecated)]
use solana_sdk::{instruction::Instruction, signature::Keypair, signer::Signer};
use txv1::TxConfig;

pub const HARNESS_TX_CONFIG: TxConfig = TxConfig::new(1_400_000, 64 * 1024 * 1024);

#[allow(clippy::result_large_err)]
pub fn send(
    svm: &mut LiteSVM,
    ixs: &[Instruction],
    signers: &[&Keypair],
) -> Result<TransactionMetadata, FailedTransactionMetadata> {
    send_with_config(svm, ixs, signers, &HARNESS_TX_CONFIG)
}

#[allow(clippy::result_large_err)]
pub fn send_with_config(
    svm: &mut LiteSVM,
    ixs: &[Instruction],
    signers: &[&Keypair],
    cfg: &TxConfig,
) -> Result<TransactionMetadata, FailedTransactionMetadata> {
    let payer = signers.first().expect("send requires at least one signer");
    let transaction =
        txv1::build_signed(&payer.pubkey(), ixs, signers, svm.latest_blockhash(), cfg)
            .expect("v1 transaction should build");
    svm.send_transaction(transaction)
}

#[track_caller]
pub fn send_expect_err(
    svm: &mut LiteSVM,
    ixs: &[Instruction],
    signers: &[&Keypair],
) -> FailedTransactionMetadata {
    send(svm, ixs, signers).expect_err("expected transaction to fail")
}

#[track_caller]
pub fn send_expect_ok(
    svm: &mut LiteSVM,
    ixs: &[Instruction],
    signers: &[&Keypair],
) -> TransactionMetadata {
    send(svm, ixs, signers).unwrap_or_else(|failed| {
        panic!(
            "expected success, got {:?}\n{}",
            failed.err,
            failed.meta.pretty_logs()
        )
    })
}
