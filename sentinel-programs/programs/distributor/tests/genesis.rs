use distributor_test_support::{
    PROGRAM_ID, assert_distributor_error, assert_tx_within_limit, close_bitmap_ix,
    deploy_upgradeable_distributor, finalize_genesis_ix, genesis_leaf_counts, genesis_roots,
    genesis_up_to_bitmaps, genesis_up_to_bitmaps_oversupplied, genesis_up_to_bitmaps_unsealed,
    initialize_bitmap_ix, initialize_config_ix, initialize_roots_ix, mint_and_accounts,
    read_bitmap, read_config, read_roots, read_token_amount, resend_expect_err, send,
    send_expect_err, set_total_claimable_ix, sweep_vault_excess_ix, write_roots_chunk_ix,
};
use litesvm::LiteSVM;
use solana_sdk::{signature::Keypair, signer::Signer};

#[test]
fn genesis_rejects_non_authority() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let attacker = Keypair::new();
    for signer in [&authority, &attacker] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    deploy_upgradeable_distributor(&mut svm, &authority);
    let mint_accounts = mint_and_accounts(&mut svm, &authority, 400, 600, &PROGRAM_ID);
    let err = send_expect_err(
        &mut svm,
        &attacker,
        initialize_config_ix(&attacker, &mint_accounts, 600),
    );
    assert_distributor_error(&err, distributor::error::DistributorError::Unauthorized);
}

#[test]
fn chunked_roots_round_trip_and_fit_the_wire_limit() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    svm.airdrop(&authority.pubkey(), 100_000_000_000).unwrap();
    deploy_upgradeable_distributor(&mut svm, &authority);
    let mint_accounts = mint_and_accounts(&mut svm, &authority, 400, 600, &PROGRAM_ID);

    send(
        &mut svm,
        &authority,
        initialize_config_ix(&authority, &mint_accounts, 600),
    );
    send(&mut svm, &authority, initialize_roots_ix(&authority));

    let roots = genesis_roots();
    let mut chunks = 0usize;
    for (index, chunk) in roots.chunks(26).enumerate() {
        let instruction =
            write_roots_chunk_ix(&authority, u16::try_from(index * 26).unwrap(), chunk);
        assert_tx_within_limit(std::slice::from_ref(&instruction), &authority.pubkey());
        send(&mut svm, &authority, instruction);
        chunks += 1;
    }
    assert_eq!(chunks, 5);

    assert_eq!(read_roots(&svm), roots);
    let config = read_config(&svm);
    assert_eq!(config.total_claimable, 600);
    assert!(!config.finalized);
    assert_eq!(config.bitmap_count, 0);
}

#[test]
fn write_roots_chunk_rejects_out_of_range_start() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    svm.airdrop(&authority.pubkey(), 100_000_000_000).unwrap();
    deploy_upgradeable_distributor(&mut svm, &authority);
    let mint_accounts = mint_and_accounts(&mut svm, &authority, 400, 600, &PROGRAM_ID);
    send(
        &mut svm,
        &authority,
        initialize_config_ix(&authority, &mint_accounts, 600),
    );
    send(&mut svm, &authority, initialize_roots_ix(&authority));
    let err = send_expect_err(
        &mut svm,
        &authority,
        write_roots_chunk_ix(
            &authority,
            distributor_core::consts::SHARD_COUNT - 1,
            &[[1u8; 32]; 2],
        ),
    );
    assert_distributor_error(&err, distributor::error::DistributorError::ChunkOutOfRange);
}

#[test]
fn initialize_bitmap_creates_zeroed_shard_bitmap_and_counts() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    svm.airdrop(&authority.pubkey(), 100_000_000_000).unwrap();
    deploy_upgradeable_distributor(&mut svm, &authority);
    let mint_accounts = mint_and_accounts(&mut svm, &authority, 400, 600, &PROGRAM_ID);
    send(
        &mut svm,
        &authority,
        initialize_config_ix(&authority, &mint_accounts, 600),
    );
    send(
        &mut svm,
        &authority,
        initialize_bitmap_ix(&authority, 5, 100),
    );
    let bitmap = read_bitmap(&svm, 5);
    assert_eq!(bitmap.shard_id, 5);
    assert_eq!(bitmap.bits.len(), 100usize.div_ceil(8));
    assert!(bitmap.bits.iter().all(|&byte| byte == 0));
    assert_eq!(read_config(&svm).bitmap_count, 1);

    let err = send_expect_err(
        &mut svm,
        &authority,
        initialize_bitmap_ix(&authority, distributor_core::consts::SHARD_COUNT, 100),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::ShardIdOutOfRange,
    );

    send(
        &mut svm,
        &authority,
        initialize_bitmap_ix(
            &authority,
            6,
            u32::try_from(distributor_merkle::sharded::MAX_SHARD_LEAF_COUNT).unwrap(),
        ),
    );
    assert_eq!(
        read_bitmap(&svm, 6).bits.len(),
        distributor_merkle::sharded::MAX_SHARD_LEAF_COUNT / 8
    );
}

#[test]
fn genesis_happy_path_finalizes_genesis_with_reconciliation() {
    let mut svm = LiteSVM::new();
    let (authority, mint_accounts, roots) = genesis_up_to_bitmaps(&mut svm, 400, 600, 600);
    let expected_roots_commitment = distributor_test_support::genesis_roots_commitment(&roots, 600);
    send(
        &mut svm,
        &authority,
        finalize_genesis_ix(&authority, &mint_accounts, expected_roots_commitment),
    );

    let config = read_config(&svm);
    assert!(config.finalized);
    assert_eq!(config.total_claimable, 600);
    assert_eq!(config.roots_commitment, expected_roots_commitment);
    assert_eq!(config.bitmap_count, distributor_core::consts::SHARD_COUNT);
}

#[test]
fn finalize_genesis_rejects_wrong_roots_commitment() {
    let mut svm = LiteSVM::new();
    let (authority, mint_accounts, roots) = genesis_up_to_bitmaps(&mut svm, 400, 600, 600);
    let wrong = [9u8; 32];
    assert_ne!(
        wrong,
        distributor_test_support::genesis_roots_commitment(&roots, 600)
    );
    let err = send_expect_err(
        &mut svm,
        &authority,
        finalize_genesis_ix(&authority, &mint_accounts, wrong),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::RootsCommitmentMismatch,
    );
}

#[test]
fn finalize_genesis_still_rejects_a_vault_funded_to_the_wrong_total() {
    let mut svm = LiteSVM::new();
    let (authority, mint_accounts, roots) = genesis_up_to_bitmaps(&mut svm, 400, 600, 500);
    let err = send_expect_err(
        &mut svm,
        &authority,
        finalize_genesis_ix(
            &authority,
            &mint_accounts,
            distributor_test_support::genesis_roots_commitment(&roots, 500),
        ),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::VaultBalanceMismatch,
    );
}

#[test]
fn finalize_genesis_rejects_supply_mismatch() {
    let mut svm = LiteSVM::new();
    let (authority, mint_accounts, roots) =
        genesis_up_to_bitmaps_oversupplied(&mut svm, 400, 600, 600, 50);
    let err = send_expect_err(
        &mut svm,
        &authority,
        finalize_genesis_ix(
            &authority,
            &mint_accounts,
            distributor_test_support::genesis_roots_commitment(&roots, 600),
        ),
    );
    assert_distributor_error(&err, distributor::error::DistributorError::SupplyMismatch);
}

#[test]
fn finalize_genesis_rejects_unsealed_mint() {
    let mut svm = LiteSVM::new();
    let (authority, mint_accounts, roots) = genesis_up_to_bitmaps_unsealed(&mut svm, 400, 600, 600);
    let err = send_expect_err(
        &mut svm,
        &authority,
        finalize_genesis_ix(
            &authority,
            &mint_accounts,
            distributor_test_support::genesis_roots_commitment(&roots, 600),
        ),
    );
    assert_distributor_error(&err, distributor::error::DistributorError::MintNotSealed);
}

#[test]
fn set_total_claimable_rewrites_before_finalize_genesis_and_is_refused_after() {
    let mut svm = LiteSVM::new();
    let (authority, mint_accounts, roots) = genesis_up_to_bitmaps(&mut svm, 400, 600, 500);
    let err = send_expect_err(
        &mut svm,
        &authority,
        finalize_genesis_ix(
            &authority,
            &mint_accounts,
            distributor_test_support::genesis_roots_commitment(&roots, 500),
        ),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::VaultBalanceMismatch,
    );

    send(
        &mut svm,
        &authority,
        set_total_claimable_ix(&authority, 600),
    );
    assert_eq!(read_config(&svm).total_claimable, 600);
    send(
        &mut svm,
        &authority,
        finalize_genesis_ix(
            &authority,
            &mint_accounts,
            distributor_test_support::genesis_roots_commitment(&roots, 600),
        ),
    );
    assert!(read_config(&svm).finalized);

    let err = send_expect_err(&mut svm, &authority, set_total_claimable_ix(&authority, 1));
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::ConfigAlreadyFinalized,
    );
}

fn genesis_with_leaf_counts(
    svm: &mut LiteSVM,
    counts: &[u32],
) -> (
    Keypair,
    distributor_test_support::MintAccounts,
    [[u8; 32]; distributor_core::consts::SHARD_COUNT as usize],
) {
    let authority = Keypair::new();
    svm.airdrop(&authority.pubkey(), 100_000_000_000).unwrap();
    deploy_upgradeable_distributor(svm, &authority);
    let mint_accounts = mint_and_accounts(svm, &authority, 400, 600, &PROGRAM_ID);
    send(
        svm,
        &authority,
        initialize_config_ix(&authority, &mint_accounts, 600),
    );
    send(svm, &authority, initialize_roots_ix(&authority));
    let roots = genesis_roots();
    for (index, chunk) in roots.chunks(26).enumerate() {
        send(
            svm,
            &authority,
            write_roots_chunk_ix(&authority, u16::try_from(index * 26).unwrap(), chunk),
        );
    }
    for (shard, &count) in counts.iter().enumerate() {
        send(
            svm,
            &authority,
            initialize_bitmap_ix(&authority, u16::try_from(shard).unwrap(), count),
        );
    }
    (authority, mint_accounts, roots)
}

fn mutate_token_account(
    svm: &mut LiteSVM,
    address: solana_sdk::pubkey::Pubkey,
    mutate: impl FnOnce(&mut spl_token_2022_interface::state::Account),
) {
    use spl_token_2022_interface::extension::StateWithExtensionsMut;
    let mut account = svm.get_account(&address).unwrap();
    {
        let mut state = StateWithExtensionsMut::<spl_token_2022_interface::state::Account>::unpack(
            &mut account.data,
        )
        .unwrap();
        mutate(&mut state.base);
        state.pack_base();
    }
    svm.set_account(address, account).unwrap();
}

#[test]
fn finalize_genesis_rejects_an_undersized_bitmap() {
    let mut svm = LiteSVM::new();
    let mut attested = vec![0u32; distributor_core::consts::SHARD_COUNT as usize];
    attested[3] = 400;
    let mut on_chain = attested.clone();
    on_chain[3] = 399;
    let (authority, mint_accounts, roots) = genesis_with_leaf_counts(&mut svm, &on_chain);
    let err = send_expect_err(
        &mut svm,
        &authority,
        finalize_genesis_ix(
            &authority,
            &mint_accounts,
            distributor_merkle::roots_commitment(&roots, &attested, 600),
        ),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::RootsCommitmentMismatch,
    );

    let mut svm = LiteSVM::new();
    let (authority, mint_accounts, roots) = genesis_with_leaf_counts(&mut svm, &attested);
    send(
        &mut svm,
        &authority,
        finalize_genesis_ix(
            &authority,
            &mint_accounts,
            distributor_merkle::roots_commitment(&roots, &attested, 600),
        ),
    );
    assert!(read_config(&svm).finalized);
}

#[test]
fn finalize_genesis_rejects_a_mint_carrying_a_forbidden_extension() {
    use spl_token_2022_interface::extension::ExtensionType;
    use spl_token_2022_interface::id as token_2022_id;

    type ExtCase = (
        &'static str,
        ExtensionType,
        fn(
            &solana_sdk::pubkey::Pubkey,
            &solana_sdk::pubkey::Pubkey,
        ) -> Vec<solana_sdk::instruction::Instruction>,
    );
    let cases: [ExtCase; 5] = [
        (
            "PermanentDelegate",
            ExtensionType::PermanentDelegate,
            |mint, auth| {
                vec![
                    spl_token_2022_interface::instruction::initialize_permanent_delegate(
                        &token_2022_id(),
                        mint,
                        auth,
                    )
                    .unwrap(),
                ]
            },
        ),
        (
            "TransferFeeConfig",
            ExtensionType::TransferFeeConfig,
            |mint, auth| {
                vec![
                spl_token_2022_interface::extension::transfer_fee::instruction::initialize_transfer_fee_config(
                    &token_2022_id(),
                    mint,
                    Some(auth),
                    Some(auth),
                    100,
                    1_000,
                )
                .unwrap(),
            ]
            },
        ),
        ("TransferHook", ExtensionType::TransferHook, |mint, auth| {
            vec![
                spl_token_2022_interface::extension::transfer_hook::instruction::initialize(
                    &token_2022_id(),
                    mint,
                    Some(*auth),
                    Some(*auth),
                )
                .unwrap(),
            ]
        }),
        ("Pausable", ExtensionType::Pausable, |mint, auth| {
            vec![
                spl_token_2022_interface::extension::pausable::instruction::initialize(
                    &token_2022_id(),
                    mint,
                    auth,
                )
                .unwrap(),
            ]
        }),
        (
            "DefaultAccountState",
            ExtensionType::DefaultAccountState,
            |mint, _auth| {
                vec![
                spl_token_2022_interface::extension::default_account_state::instruction::initialize_default_account_state(
                    &token_2022_id(),
                    mint,
                    &spl_token_2022_interface::state::AccountState::Initialized,
                )
                .unwrap(),
            ]
            },
        ),
    ];

    for (name, ext, build) in cases {
        let mut svm = LiteSVM::new();
        let (authority, mint_accounts, roots) =
            distributor_test_support::genesis_up_to_bitmaps_with(
                &mut svm,
                400,
                600,
                600,
                |svm, auth, treasury_amount, vault_amount, program_id| {
                    distributor_test_support::mint_and_accounts_with_extensions(
                        svm,
                        auth,
                        treasury_amount,
                        vault_amount,
                        program_id,
                        &[ext],
                        build,
                    )
                },
            );
        assert!(
            distributor_test_support::mint_extension_types(&svm, &mint_accounts.mint)
                .contains(&ext),
            "{name} mint should actually carry the extension under test"
        );
        let err = send_expect_err(
            &mut svm,
            &authority,
            finalize_genesis_ix(
                &authority,
                &mint_accounts,
                distributor_test_support::genesis_roots_commitment(&roots, 600),
            ),
        );
        assert_distributor_error(
            &err,
            distributor::error::DistributorError::MintExtensionForbidden,
        );
        let _ = name;
    }
}

#[test]
fn finalize_genesis_rejects_missing_bitmap() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    svm.airdrop(&authority.pubkey(), 100_000_000_000).unwrap();
    deploy_upgradeable_distributor(&mut svm, &authority);
    let mint_accounts = mint_and_accounts(&mut svm, &authority, 400, 600, &PROGRAM_ID);
    send(
        &mut svm,
        &authority,
        initialize_config_ix(&authority, &mint_accounts, 600),
    );
    send(&mut svm, &authority, initialize_roots_ix(&authority));
    let roots = genesis_roots();
    for (index, chunk) in roots.chunks(26).enumerate() {
        send(
            &mut svm,
            &authority,
            write_roots_chunk_ix(&authority, u16::try_from(index * 26).unwrap(), chunk),
        );
    }

    for shard in 1..distributor_core::consts::SHARD_COUNT {
        send(
            &mut svm,
            &authority,
            initialize_bitmap_ix(
                &authority,
                shard,
                distributor_test_support::GENESIS_BITMAP_LEAF_COUNT,
            ),
        );
    }
    assert_eq!(
        read_config(&svm).bitmap_count,
        distributor_core::consts::SHARD_COUNT - 1
    );

    let mut counts = genesis_leaf_counts();
    counts[0] = 0;
    let err = send_expect_err(
        &mut svm,
        &authority,
        finalize_genesis_ix(
            &authority,
            &mint_accounts,
            distributor_merkle::roots_commitment(&roots, &counts, 600),
        ),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::BitmapCountMismatch,
    );
}

#[test]
fn finalize_genesis_rejects_a_frozen_vault() {
    let mut svm = LiteSVM::new();
    let (authority, mint_accounts, roots) = genesis_up_to_bitmaps(&mut svm, 400, 600, 600);
    mutate_token_account(&mut svm, mint_accounts.vault, |token_account| {
        token_account.state = spl_token_2022_interface::state::AccountState::Frozen;
    });
    let err = send_expect_err(
        &mut svm,
        &authority,
        finalize_genesis_ix(
            &authority,
            &mint_accounts,
            distributor_test_support::genesis_roots_commitment(&roots, 600),
        ),
    );
    assert_distributor_error(&err, distributor::error::DistributorError::VaultNotClean);
}

#[test]
fn close_bitmap_lets_a_shard_be_resized_then_finalize_genesis_passes() {
    let mut svm = LiteSVM::new();
    let (authority, mint_accounts, roots) = genesis_up_to_bitmaps(&mut svm, 400, 600, 600);
    let counts = genesis_leaf_counts();
    let shard: u16 = 3;
    let right = counts[usize::from(shard)];

    send(&mut svm, &authority, close_bitmap_ix(&authority, shard));
    let config = read_config(&svm);
    assert_eq!(
        config.bitmap_count,
        distributor_core::consts::SHARD_COUNT - 1
    );
    assert_eq!(config.leaf_counts[usize::from(shard)], 0);
    send(
        &mut svm,
        &authority,
        initialize_bitmap_ix(&authority, shard, right.saturating_sub(1).max(1)),
    );
    let err = send_expect_err(
        &mut svm,
        &authority,
        finalize_genesis_ix(
            &authority,
            &mint_accounts,
            distributor_test_support::genesis_roots_commitment(&roots, 600),
        ),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::RootsCommitmentMismatch,
    );

    svm.expire_blockhash();
    send(&mut svm, &authority, close_bitmap_ix(&authority, shard));
    svm.expire_blockhash();
    send(
        &mut svm,
        &authority,
        initialize_bitmap_ix(&authority, shard, right),
    );
    assert_eq!(
        read_config(&svm).bitmap_count,
        distributor_core::consts::SHARD_COUNT
    );
    send(
        &mut svm,
        &authority,
        finalize_genesis_ix(
            &authority,
            &mint_accounts,
            distributor_test_support::genesis_roots_commitment(&roots, 600),
        ),
    );
    assert!(read_config(&svm).finalized);

    let err = send_expect_err(&mut svm, &authority, close_bitmap_ix(&authority, shard));
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::ConfigAlreadyFinalized,
    );
}

#[test]
fn post_finalize_genesis_instructions_are_rejected() {
    let mut svm = LiteSVM::new();
    let (authority, mint_accounts, roots) = genesis_up_to_bitmaps(&mut svm, 400, 600, 600);
    send(
        &mut svm,
        &authority,
        finalize_genesis_ix(
            &authority,
            &mint_accounts,
            distributor_test_support::genesis_roots_commitment(&roots, 600),
        ),
    );
    assert!(read_config(&svm).finalized);

    let err = send_expect_err(
        &mut svm,
        &authority,
        write_roots_chunk_ix(&authority, 0, &[[1u8; 32]; 1]),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::ConfigAlreadyFinalized,
    );

    let err = send_expect_err(
        &mut svm,
        &authority,
        initialize_bitmap_ix(&authority, 200, 8),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::ConfigAlreadyFinalized,
    );

    let err = resend_expect_err(
        &mut svm,
        &authority,
        finalize_genesis_ix(
            &authority,
            &mint_accounts,
            distributor_test_support::genesis_roots_commitment(&roots, 600),
        ),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::ConfigAlreadyFinalized,
    );
}

#[test]
fn sweep_vault_excess_moves_only_the_excess_and_is_refused_after_finalize_genesis() {
    let mut svm = LiteSVM::new();

    let (authority, mint_accounts, roots) = genesis_up_to_bitmaps(&mut svm, 400, 600, 500);
    send(
        &mut svm,
        &authority,
        sweep_vault_excess_ix(&authority, &mint_accounts),
    );
    assert_eq!(read_token_amount(&svm, &mint_accounts.vault), 500);
    assert_eq!(read_token_amount(&svm, &mint_accounts.treasury), 500);

    let err = resend_expect_err(
        &mut svm,
        &authority,
        sweep_vault_excess_ix(&authority, &mint_accounts),
    );
    assert_distributor_error(&err, distributor::error::DistributorError::VaultExcessZero);

    send(
        &mut svm,
        &authority,
        finalize_genesis_ix(
            &authority,
            &mint_accounts,
            distributor_test_support::genesis_roots_commitment(&roots, 500),
        ),
    );
    svm.expire_blockhash();
    let err = send_expect_err(
        &mut svm,
        &authority,
        sweep_vault_excess_ix(&authority, &mint_accounts),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::ConfigAlreadyFinalized,
    );
}

#[test]
fn correction_instructions_reject_non_authority() {
    let mut svm = LiteSVM::new();
    let (_authority, mint_accounts, _roots) = genesis_up_to_bitmaps(&mut svm, 400, 600, 500);
    let attacker = Keypair::new();
    svm.airdrop(&attacker.pubkey(), 100_000_000_000).unwrap();
    for instruction in [
        close_bitmap_ix(&attacker, 0),
        sweep_vault_excess_ix(&attacker, &mint_accounts),
    ] {
        let err = send_expect_err(&mut svm, &attacker, instruction);
        assert_distributor_error(&err, distributor::error::DistributorError::Unauthorized);
    }
}
