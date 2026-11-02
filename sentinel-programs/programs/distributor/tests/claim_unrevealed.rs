use distributor_test_support::{
    CosmosKey, PROGRAM_ID, assert_distributor_error, build_unrevealed_leaf_setup, claim_ix,
    read_token_amount, send, send_expect_err, sign_claim,
};
use litesvm::LiteSVM;
use solana_sdk::{signature::Keypair, signer::Signer};

#[test]
fn unrevealed_leaf_claims_via_hash160_identity() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }

    let cosmos = CosmosKey::from_seed(7);
    let setup = build_unrevealed_leaf_setup(&mut svm, &authority, &cosmos, 600, 0);
    let sig = sign_claim(
        &cosmos,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    send(
        &mut svm,
        &payer,
        claim_ix(&payer, &beneficiary.pubkey(), &setup, &sig, 600, 0, None),
    );

    assert_eq!(
        read_token_amount(&svm, &setup.beneficiary_token(&beneficiary.pubkey())),
        600
    );
    assert_eq!(read_token_amount(&svm, &setup.vault), 0);
}

#[test]
fn attacker_key_for_victim_address_is_rejected() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }

    let victim = CosmosKey::from_seed(7);
    let setup = build_unrevealed_leaf_setup(&mut svm, &authority, &victim, 600, 0);

    let attacker = CosmosKey::from_seed(99);
    let sig = sign_claim(
        &attacker,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    let err = send_expect_err(
        &mut svm,
        &payer,
        claim_ix(&payer, &beneficiary.pubkey(), &setup, &sig, 600, 0, None),
    );
    assert_distributor_error(&err, distributor::error::DistributorError::InvalidProof);
}
