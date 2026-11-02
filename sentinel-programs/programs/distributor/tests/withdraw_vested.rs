use anchor_lang::AccountSerialize;
use distributor_test_support::{
    CosmosKey, PROGRAM_ID, assert_anchor_error, assert_distributor_error, assert_event,
    build_leaf_setup_with_schedule, claim_ix, continuous_schedule, find_vesting_state_address,
    read_token_amount, read_vesting_state, resend_expect_err, send, send_expect_err, set_clock,
    sign_claim, withdraw_vested_ix,
};
use litesvm::LiteSVM;
use solana_sdk::{signature::Keypair, signer::Signer};

fn setup_vesting_state(
    svm: &mut LiteSVM,
    authority: &Keypair,
    payer: &Keypair,
    beneficiary: &Keypair,
    cosmos: &CosmosKey,
) -> distributor_test_support::SingleLeafSetup {
    let schedule = continuous_schedule(400, 1_000, 2_000);
    let setup = build_leaf_setup_with_schedule(svm, authority, cosmos, 100, 400, &schedule);
    let sig = sign_claim(
        cosmos,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    send(
        svm,
        payer,
        claim_ix(
            payer,
            &beneficiary.pubkey(),
            &setup,
            &sig,
            100,
            400,
            Some(schedule),
        ),
    );
    setup
}

#[test]
fn partial_then_full_withdraw_advances_and_closes() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    set_clock(&mut svm, 1_000);
    let cosmos = CosmosKey::from_seed(7);
    let setup = setup_vesting_state(&mut svm, &authority, &payer, &beneficiary, &cosmos);
    let pda = find_vesting_state_address(setup.shard_id, setup.leaf_index);

    let payer_balance_before = svm.get_account(&payer.pubkey()).unwrap().lamports;

    set_clock(&mut svm, 1_500);
    let meta = send(
        &mut svm,
        &payer,
        withdraw_vested_ix(&payer, &beneficiary.pubkey(), &payer.pubkey(), &setup),
    );
    assert_event(
        &meta,
        &distributor::events::VestedWithdrawn {
            shard_id: setup.shard_id,
            leaf_index: setup.leaf_index,
            beneficiary: beneficiary.pubkey(),
            amount: 200,
            closed: false,
        },
    );
    assert_eq!(
        read_token_amount(&svm, &setup.beneficiary_token(&beneficiary.pubkey())),
        300
    );
    assert_eq!(read_token_amount(&svm, &setup.vault), 200);
    let vesting_state = read_vesting_state(&svm, pda);
    assert_eq!(vesting_state.withdrawn, 200);
    assert_eq!(vesting_state.total, 400);

    let err = resend_expect_err(
        &mut svm,
        &payer,
        withdraw_vested_ix(&payer, &beneficiary.pubkey(), &payer.pubkey(), &setup),
    );
    assert_distributor_error(&err, distributor::error::DistributorError::WithdrawableZero);

    set_clock(&mut svm, 3_000);
    svm.expire_blockhash();
    send(
        &mut svm,
        &payer,
        withdraw_vested_ix(&payer, &beneficiary.pubkey(), &payer.pubkey(), &setup),
    );
    assert_eq!(
        read_token_amount(&svm, &setup.beneficiary_token(&beneficiary.pubkey())),
        500
    );
    assert_eq!(read_token_amount(&svm, &setup.vault), 0);
    assert!(
        svm.get_account(&pda).is_none(),
        "fully-drained VestingState must be closed"
    );
    let payer_balance_after = svm.get_account(&payer.pubkey()).unwrap().lamports;
    assert!(
        payer_balance_after > payer_balance_before,
        "rent must be returned to the payer (rent_payer) on close"
    );
}

#[test]
fn withdraw_rejects_substituted_beneficiary_or_rent_payer() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    let stranger = Keypair::new();
    for signer in [&authority, &payer, &stranger] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    set_clock(&mut svm, 1_000);
    let cosmos = CosmosKey::from_seed(7);
    let setup = setup_vesting_state(&mut svm, &authority, &payer, &beneficiary, &cosmos);
    set_clock(&mut svm, 1_500);

    let err = send_expect_err(
        &mut svm,
        &payer,
        withdraw_vested_ix(&payer, &stranger.pubkey(), &payer.pubkey(), &setup),
    );
    assert_anchor_error(&err, anchor_lang::error::ErrorCode::ConstraintAddress);

    let err = send_expect_err(
        &mut svm,
        &payer,
        withdraw_vested_ix(&payer, &beneficiary.pubkey(), &stranger.pubkey(), &setup),
    );
    assert_anchor_error(&err, anchor_lang::error::ErrorCode::ConstraintAddress);
}

#[test]
fn withdraw_rejects_a_schedule_that_no_longer_validates() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    set_clock(&mut svm, 1_000);
    let cosmos = CosmosKey::from_seed(7);
    let setup = setup_vesting_state(&mut svm, &authority, &payer, &beneficiary, &cosmos);
    let pda = find_vesting_state_address(setup.shard_id, setup.leaf_index);

    let mut vesting_state = read_vesting_state(&svm, pda);
    vesting_state.schedule.end_at = vesting_state.schedule.start_at;
    let mut data = Vec::new();
    vesting_state.try_serialize(&mut data).unwrap();
    let mut account = svm.get_account(&pda).unwrap();
    account.data = data;
    svm.set_account(pda, account).unwrap();

    set_clock(&mut svm, 1_500);
    let err = send_expect_err(
        &mut svm,
        &payer,
        withdraw_vested_ix(&payer, &beneficiary.pubkey(), &payer.pubkey(), &setup),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::InvalidStoredSchedule,
    );
}
