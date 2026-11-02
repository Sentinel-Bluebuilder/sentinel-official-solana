use anchor_lang::{InstructionData, ToAccountMetas};
use ceremony::sender::{MAX_CU_LIMIT, MAX_LOADED_ACCOUNTS_DATA_BYTES, ROOTS_PER_CHUNK};
use ceremony::wire_size::{check_tx_within_limit, tx_wire_bytes};
use solana_sdk::{instruction::Instruction, pubkey::Pubkey};
use txv1::{MAX_TX_BYTES, TxConfig};

fn tx_config() -> TxConfig {
    TxConfig::new(MAX_CU_LIMIT, MAX_LOADED_ACCOUNTS_DATA_BYTES).with_priority_fee_lamports(1)
}

fn noop_ix(data_len: usize) -> Instruction {
    Instruction {
        program_id: Pubkey::new_unique(),
        accounts: vec![],
        data: vec![0u8; data_len],
    }
}

#[test]
fn small_tx_is_within_limit() {
    let payer = Pubkey::new_unique();
    let ixs = [noop_ix(10)];
    assert!(tx_wire_bytes(&ixs, &payer, &tx_config()) < MAX_TX_BYTES);
    assert!(check_tx_within_limit(&ixs, &payer, &tx_config()).is_ok());
}

#[test]
fn oversized_tx_is_rejected_with_message() {
    let payer = Pubkey::new_unique();
    let ixs = [noop_ix(5000)];
    let err = check_tx_within_limit(&ixs, &payer, &tx_config())
        .unwrap_err()
        .to_string();
    assert!(err.contains("4096"), "message should cite the limit: {err}");
}

fn write_roots_chunk_ix(upgrade_authority: Pubkey, roots_count: usize) -> Instruction {
    #[allow(clippy::cast_possible_truncation)]
    let chunk: Vec<[u8; 32]> = (0..roots_count).map(|index| [index as u8; 32]).collect();
    Instruction {
        program_id: distributor::ID,
        accounts: distributor::accounts::WriteRootsChunk {
            upgrade_authority,
            program: distributor::ID,
            program_data: Pubkey::new_unique(),
            config: Pubkey::new_unique(),
            roots: Pubkey::new_unique(),
        }
        .to_account_metas(None),
        data: distributor::instruction::WriteRootsChunk {
            start_index: 0,
            chunk,
        }
        .data(),
    }
}

#[test]
fn roots_per_chunk_fits_the_wire_limit() {
    let payer = Pubkey::new_unique();
    let upgrade_authority = Pubkey::new_unique();

    let instruction = write_roots_chunk_ix(upgrade_authority, ROOTS_PER_CHUNK);
    assert!(
        check_tx_within_limit(std::slice::from_ref(&instruction), &payer, &tx_config()).is_ok(),
        "ROOTS_PER_CHUNK={ROOTS_PER_CHUNK} overflows the v1 wire budget once the \
         distinct-payer upgrade_authority signature is included"
    );

    let ix_over = write_roots_chunk_ix(upgrade_authority, ROOTS_PER_CHUNK + 1);
    assert!(
        check_tx_within_limit(std::slice::from_ref(&ix_over), &payer, &tx_config()).is_err(),
        "ROOTS_PER_CHUNK+1 unexpectedly still fits -- the chunk size is not tight"
    );
}
