use ceremony::CeremonyError;
use ceremony::predicates::{is_config_step_done, is_metadata_step_done, is_roots_chunk_done};
use ceremony::runner::HaltedStep;
use distributor_mint::create::TokenMetadataParams;
use litesvm::LiteSVM;
use solana_keypair::Keypair;
use solana_pubkey::Pubkey;
use solana_signature::Signature;
use solana_signer::Signer;
use solana_transaction::Transaction;

fn config_bytes(svm: &LiteSVM, program_id: &Pubkey) -> Option<Vec<u8>> {
    let (config, _) = distributor_core::pda::find_config(program_id);
    svm.get_account(&config).map(|account| account.data)
}

#[test]
fn initialize_config_and_finalize_genesis_predicates_track_on_chain_state() {
    let mut fresh = LiteSVM::new();
    let upgrade_authority = Keypair::new();
    fresh
        .airdrop(&upgrade_authority.pubkey(), 1_000_000_000_000)
        .unwrap();
    let program_id =
        distributor_test_support::deploy_upgradeable_distributor(&mut fresh, &upgrade_authority);
    assert!(config_bytes(&fresh, &program_id).is_none());
    assert!(!is_config_step_done("distributor_initialize_config", None));
    assert!(!is_config_step_done("distributor_finalize_genesis", None));

    let mut mid = LiteSVM::new();
    let _ = distributor_test_support::genesis_up_to_bitmaps(&mut mid, 400, 600, 600);
    let data = config_bytes(&mid, &distributor_test_support::PROGRAM_ID).expect("config exists");
    assert!(is_config_step_done(
        "distributor_initialize_config",
        Some(&data)
    ));
    assert!(!is_config_step_done(
        "distributor_finalize_genesis",
        Some(&data)
    ));

    let mut done = LiteSVM::new();
    let second_upgrade_authority = Keypair::new();
    done.airdrop(&second_upgrade_authority.pubkey(), 1_000_000_000_000)
        .unwrap();
    let cosmos = distributor_test_support::CosmosKey::from_seed(3);
    distributor_test_support::build_single_leaf_setup(
        &mut done,
        &second_upgrade_authority,
        &cosmos,
        100,
        0,
    );
    let data = config_bytes(&done, &distributor_test_support::PROGRAM_ID).expect("config exists");
    assert!(is_config_step_done(
        "distributor_initialize_config",
        Some(&data)
    ));
    assert!(is_config_step_done(
        "distributor_finalize_genesis",
        Some(&data)
    ));
}

#[test]
fn initialize_metadata_predicate_tracks_tlv_growth() {
    use distributor_mint::create::{create_mint_ixs, mint_account_space};
    use distributor_mint::supply::{initialize_metadata_ixs, metadata_tlv_space};

    let mut svm = LiteSVM::new();
    let upgrade_authority = Keypair::new();
    svm.airdrop(&upgrade_authority.pubkey(), 1_000_000_000_000)
        .unwrap();
    let mint = Keypair::new();
    let params = TokenMetadataParams {
        name: "Sentinel".into(),
        symbol: "SENT".into(),
        uri: "https://sentinel.co/metadata.json".into(),
    };
    let mint_at_create_lamports = svm.minimum_balance_for_rent_exemption(mint_account_space());
    let mint_with_metadata_lamports =
        svm.minimum_balance_for_rent_exemption(mint_account_space() + metadata_tlv_space(&params));

    let ixs = create_mint_ixs(
        &upgrade_authority.pubkey(),
        &mint.pubkey(),
        &upgrade_authority.pubkey(),
        &upgrade_authority.pubkey(),
        mint_at_create_lamports,
    )
    .expect("upgrade_authority is never the zero address");
    let transaction = Transaction::new_signed_with_payer(
        &ixs,
        Some(&upgrade_authority.pubkey()),
        &[&upgrade_authority, &mint],
        svm.latest_blockhash(),
    );
    svm.send_transaction(transaction)
        .expect("create_mint should land");
    let before = svm.get_account(&mint.pubkey()).map(|account| account.data);
    assert!(before.is_some());
    assert!(!is_metadata_step_done(before.as_deref()));

    let ixs = initialize_metadata_ixs(
        &upgrade_authority.pubkey(),
        &mint.pubkey(),
        &upgrade_authority.pubkey(),
        &upgrade_authority.pubkey(),
        mint_at_create_lamports,
        mint_with_metadata_lamports,
        &params,
    );
    let transaction = Transaction::new_signed_with_payer(
        &ixs,
        Some(&upgrade_authority.pubkey()),
        &[&upgrade_authority],
        svm.latest_blockhash(),
    );
    svm.send_transaction(transaction)
        .expect("initialize_metadata should land");
    let after = svm.get_account(&mint.pubkey()).map(|account| account.data);
    assert!(is_metadata_step_done(after.as_deref()));
}

#[test]
fn roots_chunk_predicate_tracks_written_bytes() {
    let len = 8 + (distributor_core::consts::SHARD_COUNT as usize) * 32;
    let mut data = vec![0u8; len];

    let chunk = [[1u8; 32], [2u8; 32]];

    assert!(!is_roots_chunk_done(None, 0, &chunk));

    assert!(!is_roots_chunk_done(Some(&data), 3, &chunk));

    let start = 8 + 3 * 32;
    data[start..start + 32].copy_from_slice(&chunk[0]);
    data[start + 32..start + 64].copy_from_slice(&chunk[1]);
    assert!(is_roots_chunk_done(Some(&data), 3, &chunk));

    let empty = [[0u8; 32]];
    assert!(!is_roots_chunk_done(Some(&data), 0, &empty));

    assert!(!is_roots_chunk_done(Some(&data[..10]), 3, &chunk));
}

#[test]
fn halted_step_carries_a_typed_step_field_and_a_stable_display() {
    let signature = Signature::new_unique();
    let halted = HaltedStep {
        step: "distributor_mint_supply".to_string(),
        source: CeremonyError::TransportFailed {
            signature,
            message: "confirm timed out".to_string(),
        },
        signature: None,
        records: vec![],
    };
    assert_eq!(halted.step, "distributor_mint_supply");
    let expected = format!(
        "ceremony halted at step 'distributor_mint_supply': transaction {signature} failed: confirm timed out"
    );
    assert_eq!(halted.to_string(), expected);
    let as_string: String = halted.into();
    assert_eq!(as_string, expected);
}
