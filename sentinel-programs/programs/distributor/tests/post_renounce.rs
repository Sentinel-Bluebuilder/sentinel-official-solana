use distributor_test_support::{
    CosmosKey, PROGRAM_ID, build_leaf_setup_with_schedule, claim_ix, continuous_schedule,
    read_token_amount, renounce_upgrade_authority, send, set_clock, sign_claim, withdraw_vested_ix,
};
use litesvm::LiteSVM;
use solana_sdk::{signature::Keypair, signer::Signer};

#[test]
fn claim_and_withdraw_vested_survive_upgrade_authority_renounce() {
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

    renounce_upgrade_authority(&mut svm);

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
    assert_eq!(
        read_token_amount(&svm, &setup.beneficiary_token(&beneficiary.pubkey())),
        100
    );

    set_clock(&mut svm, 3_000);
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
}
