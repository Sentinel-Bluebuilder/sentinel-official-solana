#![allow(
    clippy::unwrap_used,
    clippy::expect_used,
    clippy::panic,
    clippy::indexing_slicing,
    clippy::arithmetic_side_effects
)]

use litesvm::LiteSVM;
use solana_hash::Hash;
use solana_instruction::error::InstructionError;
use solana_instruction::{AccountMeta, Instruction};
use solana_keypair::Keypair;
use solana_message::VersionedMessage;
use solana_pubkey::Pubkey;
use solana_signer::Signer;
use solana_system_interface::instruction::transfer;
use solana_transaction::TransactionError;
use txv1::{TxConfig, build_signed, priority_fee_lamports, unsigned_wire_size, wire_size};

const LOADED_ACCOUNTS_DATA_SIZE_LIMIT_BYTES: u32 = 64 * 1024 * 1024;

fn funded_payer(svm: &mut LiteSVM) -> Keypair {
    let payer = Keypair::new();
    svm.airdrop(&payer.pubkey(), 10_000_000_000).unwrap();
    payer
}

#[test]
fn header_cu_limit_below_usage_fails_and_sufficient_lands() {
    let mut svm = LiteSVM::new();
    let payer = funded_payer(&mut svm);
    let destination = Pubkey::new_unique();

    let starved = build_signed(
        &payer.pubkey(),
        &[transfer(&payer.pubkey(), &destination, 0)],
        &[&payer],
        svm.latest_blockhash(),
        &TxConfig::new(1, LOADED_ACCOUNTS_DATA_SIZE_LIMIT_BYTES),
    )
    .unwrap();
    let err = svm.send_transaction(starved).unwrap_err().err;
    assert!(
        matches!(
            err,
            TransactionError::InstructionError(0, InstructionError::ComputationalBudgetExceeded)
        ),
        "got {err:?}"
    );

    let ample = build_signed(
        &payer.pubkey(),
        &[transfer(&payer.pubkey(), &destination, 0)],
        &[&payer],
        svm.latest_blockhash(),
        &TxConfig::new(1_400_000, LOADED_ACCOUNTS_DATA_SIZE_LIMIT_BYTES),
    )
    .unwrap();
    let result = svm.send_transaction(ample);
    assert!(result.is_ok(), "got {result:?}");
}

#[test]
fn unsigned_wire_size_matches_wire_size_multi_sig() {
    let payer = Keypair::new();
    let cosigner = Keypair::new();
    let instruction = Instruction::new_with_bytes(
        Pubkey::new_unique(),
        &[7, 8, 9],
        vec![
            AccountMeta::new(payer.pubkey(), true),
            AccountMeta::new(cosigner.pubkey(), true),
        ],
    );
    let transaction = build_signed(
        &payer.pubkey(),
        &[instruction],
        &[&payer, &cosigner],
        Hash::default(),
        &TxConfig::new(1, 1),
    )
    .unwrap();
    let VersionedMessage::V1(msg) = &transaction.message else {
        panic!("expected v1 message, got {:?}", transaction.message);
    };
    assert_eq!(msg.header.num_required_signatures, 2);
    assert_eq!(wire_size(&transaction).unwrap(), unsigned_wire_size(msg));
}

#[test]
fn priority_fee_lamports_rounds_and_saturates() {
    assert_eq!(priority_fee_lamports(0, 400_000), 0);
    assert_eq!(priority_fee_lamports(1, 1), 1);
    assert_eq!(priority_fee_lamports(2_500_000, 3), 8);
    assert_eq!(priority_fee_lamports(u64::MAX, u32::MAX), u64::MAX);
}

#[cfg(feature = "rpc")]
#[test]
fn enable_tx_v1_id_matches_agave() {
    assert_eq!(
        txv1::preflight::ENABLE_TX_V1,
        agave_feature_set::enable_tx_v1::ID
    );
}
