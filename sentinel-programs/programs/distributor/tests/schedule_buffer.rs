use distributor_test_support::{
    CosmosKey, PROGRAM_ID, assert_distributor_error, build_leaf_setup_with_schedule,
    claim_ix_with_buffer_account, claim_ix_with_buffer_rent_payer, continuous_schedule,
    deploy_upgradeable_distributor, find_schedule_buffer_address, find_vesting_state_address,
    periodic_schedule, read_vesting_state, resend_expect_err, send, send_expect_err, set_clock,
    sign_claim, stage_abandon_ix, stage_finalize_ix, stage_init_ix, stage_schedule, stage_write_ix,
};
use litesvm::LiteSVM;
use solana_sdk::{signature::Keypair, signer::Signer};

fn big_schedule() -> distributor_vesting::schedule::Schedule {
    periodic_schedule(512_000, 10_000, 100, 512)
}

#[test]
fn stage_abandon_returns_rent_for_an_unfinalized_buffer() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    svm.airdrop(&authority.pubkey(), 100_000_000_000).unwrap();
    svm.airdrop(&payer.pubkey(), 100_000_000_000).unwrap();
    deploy_upgradeable_distributor(&mut svm, &authority);

    let schedule = continuous_schedule(100, 1_000, 2_000);
    let preimage = schedule.encode_preimage();
    send(
        &mut svm,
        &payer,
        stage_init_ix(&payer, 3, 9, u32::try_from(preimage.len()).unwrap()),
    );
    let buffer = find_schedule_buffer_address(3, 9, &payer.pubkey());
    assert!(svm.get_account(&buffer).is_some());

    let balance_before = svm.get_balance(&payer.pubkey()).unwrap();
    send(&mut svm, &payer, stage_abandon_ix(&payer, 3, 9));
    assert!(svm.get_account(&buffer).is_none());
    assert!(svm.get_balance(&payer.pubkey()).unwrap() > balance_before);
}

#[test]
fn write_after_finalize_is_rejected() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    svm.airdrop(&authority.pubkey(), 100_000_000_000).unwrap();
    svm.airdrop(&payer.pubkey(), 100_000_000_000).unwrap();
    deploy_upgradeable_distributor(&mut svm, &authority);

    let schedule = continuous_schedule(100, 1_000, 2_000);
    let preimage = schedule.encode_preimage();
    send(
        &mut svm,
        &payer,
        stage_init_ix(&payer, 0, 0, u32::try_from(preimage.len()).unwrap()),
    );
    send(
        &mut svm,
        &payer,
        stage_write_ix(&payer, 0, 0, 0, preimage.clone()),
    );
    let digest = distributor_core::hash::keccak256(&[&preimage]);
    send(&mut svm, &payer, stage_finalize_ix(&payer, 0, 0, digest));

    let err = resend_expect_err(&mut svm, &payer, stage_write_ix(&payer, 0, 0, 0, preimage));
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::BufferAlreadyFinalized,
    );
}

#[test]
fn finalize_digest_mismatch_is_rejected() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    svm.airdrop(&authority.pubkey(), 100_000_000_000).unwrap();
    svm.airdrop(&payer.pubkey(), 100_000_000_000).unwrap();
    deploy_upgradeable_distributor(&mut svm, &authority);

    let schedule = continuous_schedule(100, 1_000, 2_000);
    let preimage = schedule.encode_preimage();
    send(
        &mut svm,
        &payer,
        stage_init_ix(&payer, 1, 5, u32::try_from(preimage.len()).unwrap()),
    );
    send(&mut svm, &payer, stage_write_ix(&payer, 1, 5, 0, preimage));

    let wrong_digest = [7u8; 32];
    let err = send_expect_err(
        &mut svm,
        &payer,
        stage_finalize_ix(&payer, 1, 5, wrong_digest),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::BufferDigestMismatch,
    );
}

#[test]
fn staged_512_period_schedule_same_party_claim_consumes_and_closes_buffer() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    set_clock(&mut svm, 1_000);

    let cosmos = CosmosKey::from_seed(1);
    let schedule = big_schedule();
    let setup =
        build_leaf_setup_with_schedule(&mut svm, &authority, &cosmos, 0, schedule.total, &schedule);

    let buffer = stage_schedule(
        &mut svm,
        &payer,
        setup.shard_id,
        setup.leaf_index,
        &schedule,
    );
    assert!(svm.get_account(&buffer).is_some());

    let sig = sign_claim(
        &cosmos,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    send(
        &mut svm,
        &payer,
        claim_ix_with_buffer_rent_payer(
            &payer,
            &beneficiary.pubkey(),
            &setup,
            &sig,
            0,
            schedule.total,
            &payer.pubkey(),
        ),
    );

    assert!(svm.get_account(&buffer).is_none());

    let pda = find_vesting_state_address(setup.shard_id, setup.leaf_index);
    let vesting_state = read_vesting_state(&svm, pda);
    assert_eq!(vesting_state.schedule, schedule);
    assert_eq!(vesting_state.total, schedule.total);
}

#[test]
fn buffer_not_canonical_for_its_own_payer_is_rejected() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    set_clock(&mut svm, 1_000);

    let cosmos = CosmosKey::from_seed(3);
    let schedule = continuous_schedule(400, 1_000, 2_000);
    let setup = build_leaf_setup_with_schedule(&mut svm, &authority, &cosmos, 0, 400, &schedule);

    let bogus_leaf_index = setup.leaf_index + 1;
    let other_buffer = stage_schedule(
        &mut svm,
        &payer,
        setup.shard_id,
        bogus_leaf_index,
        &schedule,
    );

    let sig = sign_claim(
        &cosmos,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    let err = send_expect_err(
        &mut svm,
        &payer,
        claim_ix_with_buffer_account(
            &payer,
            &beneficiary.pubkey(),
            &setup,
            &sig,
            0,
            400,
            other_buffer,
        ),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::BufferSeedsMismatch,
    );
}

#[test]
fn claim_with_staged_but_not_finalized_buffer_is_rejected() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    set_clock(&mut svm, 1_000);

    let cosmos = CosmosKey::from_seed(9);
    let schedule = continuous_schedule(400, 1_000, 2_000);
    let setup = build_leaf_setup_with_schedule(&mut svm, &authority, &cosmos, 0, 400, &schedule);

    let preimage = schedule.encode_preimage();
    send(
        &mut svm,
        &payer,
        stage_init_ix(
            &payer,
            setup.shard_id,
            setup.leaf_index,
            u32::try_from(preimage.len()).unwrap(),
        ),
    );
    send(
        &mut svm,
        &payer,
        stage_write_ix(&payer, setup.shard_id, setup.leaf_index, 0, preimage),
    );

    let buffer = find_schedule_buffer_address(setup.shard_id, setup.leaf_index, &payer.pubkey());

    let sig = sign_claim(
        &cosmos,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    let err = send_expect_err(
        &mut svm,
        &payer,
        claim_ix_with_buffer_account(&payer, &beneficiary.pubkey(), &setup, &sig, 0, 400, buffer),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::BufferNotFinalized,
    );
}
