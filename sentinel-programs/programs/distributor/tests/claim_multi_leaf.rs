use distributor_test_support::{
    CosmosKey, PROGRAM_ID, SingleLeafSetup, assert_distributor_error,
    build_two_leaf_same_shard_setup, claim_ix, read_token_amount, resend_expect_err,
    same_shard_cosmos_pair, send, sign_claim,
};
use litesvm::LiteSVM;
use solana_sdk::{signature::Keypair, signer::Signer};

#[test]
fn claiming_one_leaf_leaves_its_shard_neighbor_claimable() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary_a = Keypair::new();
    let beneficiary_b = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }

    let (cosmos_a, cosmos_b) = same_shard_cosmos_pair();
    let (setup0, setup1) =
        build_two_leaf_same_shard_setup(&mut svm, &authority, &cosmos_a, &cosmos_b, 600);
    assert_eq!(setup0.shard_id, setup1.shard_id);
    assert_ne!(setup0.leaf_index, setup1.leaf_index);

    let key_for = |leaf_setup: &SingleLeafSetup| -> &CosmosKey {
        if leaf_setup.bech32_address == cosmos_a.bech32_address() {
            &cosmos_a
        } else {
            &cosmos_b
        }
    };

    let sig0 = sign_claim(
        key_for(&setup0),
        &beneficiary_a.pubkey(),
        &setup0.roots_commitment,
        &PROGRAM_ID,
    );
    send(
        &mut svm,
        &payer,
        claim_ix(
            &payer,
            &beneficiary_a.pubkey(),
            &setup0,
            &sig0,
            600,
            0,
            None,
        ),
    );
    assert_eq!(
        read_token_amount(&svm, &setup0.beneficiary_token(&beneficiary_a.pubkey())),
        600
    );

    let sig1 = sign_claim(
        key_for(&setup1),
        &beneficiary_b.pubkey(),
        &setup1.roots_commitment,
        &PROGRAM_ID,
    );
    send(
        &mut svm,
        &payer,
        claim_ix(
            &payer,
            &beneficiary_b.pubkey(),
            &setup1,
            &sig1,
            600,
            0,
            None,
        ),
    );
    assert_eq!(
        read_token_amount(&svm, &setup1.beneficiary_token(&beneficiary_b.pubkey())),
        600
    );
    assert_eq!(read_token_amount(&svm, &setup0.vault), 0);

    let err = resend_expect_err(
        &mut svm,
        &payer,
        claim_ix(
            &payer,
            &beneficiary_a.pubkey(),
            &setup0,
            &sig0,
            600,
            0,
            None,
        ),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::LeafAlreadyClaimed,
    );
}
