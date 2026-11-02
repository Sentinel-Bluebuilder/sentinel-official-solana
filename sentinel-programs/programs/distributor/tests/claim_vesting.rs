use distributor_test_support::{
    CosmosKey, PROGRAM_ID, assert_distributor_error, assert_event, build_leaf_setup_with_schedule,
    claim_ix, continuous_schedule, find_vesting_state_address, read_token_amount,
    read_vesting_state, send, send_expect_err, set_clock, sign_claim, transfer_lamports,
};
use litesvm::LiteSVM;
use solana_sdk::{signature::Keypair, signer::Signer};

#[test]
fn locked_claim_creates_vesting_state_and_transfers_only_liquid() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    set_clock(&mut svm, 1_000);

    let cosmos = CosmosKey::from_seed(7);
    let schedule = continuous_schedule(400, 1_000, 2_000);
    let setup = build_leaf_setup_with_schedule(&mut svm, &authority, &cosmos, 100, 400, &schedule);
    let sig = sign_claim(
        &cosmos,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    send(
        &mut svm,
        &payer,
        claim_ix(
            &payer,
            &beneficiary.pubkey(),
            &setup,
            &sig,
            100,
            400,
            Some(schedule.clone()),
        ),
    );

    assert_eq!(
        read_token_amount(&svm, &setup.beneficiary_token(&beneficiary.pubkey())),
        100
    );
    assert_eq!(read_token_amount(&svm, &setup.vault), 400);

    let pda = find_vesting_state_address(setup.shard_id, setup.leaf_index);
    let vesting_state = read_vesting_state(&svm, pda);
    assert_eq!(vesting_state.total, 400);
    assert_eq!(vesting_state.withdrawn, 0);
    assert_eq!(vesting_state.rent_payer, payer.pubkey());
    assert_eq!(vesting_state.beneficiary, beneficiary.pubkey());
    assert_eq!(
        vesting_state.compressed_public_key,
        cosmos.compressed_public_key()
    );
    assert_eq!(vesting_state.schedule, schedule);
}

#[test]
fn fully_matured_schedule_folds_into_liquid_with_no_vesting_state() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    set_clock(&mut svm, 5_000);

    let cosmos = CosmosKey::from_seed(7);
    let schedule = continuous_schedule(400, 1_000, 2_000);
    let setup = build_leaf_setup_with_schedule(&mut svm, &authority, &cosmos, 100, 400, &schedule);
    let sig = sign_claim(
        &cosmos,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    let meta = send(
        &mut svm,
        &payer,
        claim_ix(
            &payer,
            &beneficiary.pubkey(),
            &setup,
            &sig,
            100,
            400,
            Some(schedule),
        ),
    );

    assert_eq!(
        read_token_amount(&svm, &setup.beneficiary_token(&beneficiary.pubkey())),
        500
    );
    assert_eq!(read_token_amount(&svm, &setup.vault), 0);

    assert_event(
        &meta,
        &distributor::events::LeafClaimed {
            shard_id: setup.shard_id,
            leaf_index: setup.leaf_index,
            beneficiary: beneficiary.pubkey(),
            liquid_amount: 100,
            locked_amount: 400,
            folded: true,
        },
    );

    let pda = find_vesting_state_address(setup.shard_id, setup.leaf_index);
    assert!(
        svm.get_account(&pda).is_none(),
        "a fully-matured claim must not create a VestingState"
    );
}

#[test]
fn schedule_hash_mismatch_is_rejected() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    set_clock(&mut svm, 1_000);

    let cosmos = CosmosKey::from_seed(7);
    let schedule = continuous_schedule(400, 1_000, 2_000);
    let setup = build_leaf_setup_with_schedule(&mut svm, &authority, &cosmos, 100, 400, &schedule);
    let sig = sign_claim(
        &cosmos,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );

    let wrong_sched = continuous_schedule(400, 1_000, 3_000);
    let err = send_expect_err(
        &mut svm,
        &payer,
        claim_ix(
            &payer,
            &beneficiary.pubkey(),
            &setup,
            &sig,
            100,
            400,
            Some(wrong_sched),
        ),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::ScheduleHashMismatch,
    );
}

#[test]
fn prefunded_vesting_state_address_does_not_block_claim() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let griefer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer, &griefer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    set_clock(&mut svm, 1_000);
    let cosmos = CosmosKey::from_seed(7);
    let schedule = continuous_schedule(400, 1_000, 2_000);
    let setup = build_leaf_setup_with_schedule(&mut svm, &authority, &cosmos, 100, 400, &schedule);
    let pda = find_vesting_state_address(setup.shard_id, setup.leaf_index);
    let dust = svm.minimum_balance_for_rent_exemption(0);
    transfer_lamports(&mut svm, &griefer, &pda, dust);
    let sig = sign_claim(
        &cosmos,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    send(
        &mut svm,
        &payer,
        claim_ix(
            &payer,
            &beneficiary.pubkey(),
            &setup,
            &sig,
            100,
            400,
            Some(schedule),
        ),
    );
    assert_eq!(read_vesting_state(&svm, pda).total, 400);
}
