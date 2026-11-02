use distributor_test_support::{
    CosmosKey, LeafSetupSpec, PROGRAM_ID, SingleLeafSetup, assert_distributor_error, assert_event,
    build_leaf_setup_from_spec, build_single_leaf_setup, claim_ix, continuous_schedule,
    deploy_upgradeable_distributor, finalize_genesis_ix, initialize_bitmap_ix,
    initialize_config_ix, initialize_roots_ix, mint_and_accounts, read_token_amount,
    resend_expect_err, send, send_expect_err, set_clock, sign_claim, sign_claim_high_s,
    sign_claim_with_address, write_roots_chunk_ix,
};
use litesvm::LiteSVM;
use solana_sdk::{pubkey::Pubkey, signature::Keypair, signer::Signer};

#[test]
fn revealed_leaf_claim_transfers_liquid_to_beneficiary_token() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }

    let cosmos = CosmosKey::from_seed(7);
    let setup = build_single_leaf_setup(&mut svm, &authority, &cosmos, 600, 0);
    let sig = sign_claim(
        &cosmos,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    let meta = send(
        &mut svm,
        &payer,
        claim_ix(&payer, &beneficiary.pubkey(), &setup, &sig, 600, 0, None),
    );

    assert_eq!(
        read_token_amount(&svm, &setup.beneficiary_token(&beneficiary.pubkey())),
        600
    );
    assert_eq!(read_token_amount(&svm, &setup.vault), 0);
    assert_event(
        &meta,
        &distributor::events::LeafClaimed {
            shard_id: setup.shard_id,
            leaf_index: setup.leaf_index,
            beneficiary: beneficiary.pubkey(),
            liquid_amount: 600,
            locked_amount: 0,
            folded: false,
        },
    );
}

#[test]
fn double_claim_is_rejected() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    let cosmos = CosmosKey::from_seed(7);
    let setup = build_single_leaf_setup(&mut svm, &authority, &cosmos, 600, 0);
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
    let err = resend_expect_err(
        &mut svm,
        &payer,
        claim_ix(&payer, &beneficiary.pubkey(), &setup, &sig, 600, 0, None),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::LeafAlreadyClaimed,
    );
}

#[test]
fn forged_amount_fails_proof() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    let cosmos = CosmosKey::from_seed(7);
    let setup = build_single_leaf_setup(&mut svm, &authority, &cosmos, 600, 0);
    let sig = sign_claim(
        &cosmos,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    let err = send_expect_err(
        &mut svm,
        &payer,
        claim_ix(&payer, &beneficiary.pubkey(), &setup, &sig, 999, 0, None),
    );
    assert_distributor_error(&err, distributor::error::DistributorError::InvalidProof);
}

#[test]
fn claim_with_shard_id_not_matching_address_shard_is_rejected() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    let cosmos = CosmosKey::from_seed(7);
    let setup = build_single_leaf_setup(&mut svm, &authority, &cosmos, 600, 0);
    let sig = sign_claim(
        &cosmos,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    let wrong_shard = (setup.shard_id + 1) % distributor_core::consts::SHARD_COUNT;
    let forged_setup = SingleLeafSetup {
        shard_id: wrong_shard,
        leaf_index: setup.leaf_index,
        bech32_address: setup.bech32_address.clone(),
        vesting_schedule_hash: setup.vesting_schedule_hash,
        proof: setup.proof.clone(),
        roots_commitment: setup.roots_commitment,
        vault: setup.vault,
        mint: setup.mint,
    };
    let err = send_expect_err(
        &mut svm,
        &payer,
        claim_ix(
            &payer,
            &beneficiary.pubkey(),
            &forged_setup,
            &sig,
            600,
            0,
            None,
        ),
    );
    assert_distributor_error(&err, distributor::error::DistributorError::ShardIdMismatch);
}

#[test]
fn signature_for_other_beneficiary_fails() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    let attacker = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    let cosmos = CosmosKey::from_seed(7);
    let setup = build_single_leaf_setup(&mut svm, &authority, &cosmos, 600, 0);
    let sig = sign_claim(
        &cosmos,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    let err = send_expect_err(
        &mut svm,
        &payer,
        claim_ix(&payer, &attacker.pubkey(), &setup, &sig, 600, 0, None),
    );
    assert_distributor_error(&err, distributor::error::DistributorError::InvalidProof);
}

#[test]
fn claim_before_finalize_genesis_is_rejected() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }

    let cosmos = CosmosKey::from_seed(7);
    let record = distributor_merkle::LedgerRecord {
        compressed_public_key: Some(cosmos.compressed_public_key()),
        bech32_address: cosmos.bech32_address(),
        liquid_amount: 600,
        locked_amount: 0,
        vesting_schedule_hash: distributor_vesting::schedule::ZERO_SCHEDULE_HASH,
    };
    let tree = distributor_merkle::build_sharded_tree(vec![record])
        .expect("single-leaf tree should build");
    let leaf = &tree.leaves[0];

    deploy_upgradeable_distributor(&mut svm, &authority);
    let mint_accounts = mint_and_accounts(&mut svm, &authority, 1_000_000, 600, &PROGRAM_ID);
    send(
        &mut svm,
        &authority,
        initialize_config_ix(&authority, &mint_accounts, 600),
    );
    send(&mut svm, &authority, initialize_roots_ix(&authority));
    for (index, chunk) in tree.roots.chunks(26).enumerate() {
        send(
            &mut svm,
            &authority,
            write_roots_chunk_ix(&authority, u16::try_from(index * 26).unwrap(), chunk),
        );
    }
    for shard in 0..distributor_core::consts::SHARD_COUNT {
        let leaf_count = if shard == leaf.shard_id { 1 } else { 0 };
        send(
            &mut svm,
            &authority,
            initialize_bitmap_ix(&authority, shard, leaf_count),
        );
    }

    let setup = SingleLeafSetup {
        shard_id: leaf.shard_id,
        leaf_index: leaf.leaf_index,
        bech32_address: leaf.bech32_address.clone(),
        vesting_schedule_hash: leaf.vesting_schedule_hash,
        proof: leaf.proof.clone(),
        roots_commitment: tree.roots_commitment,
        vault: mint_accounts.vault,
        mint: mint_accounts.mint,
    };
    let sig = sign_claim(
        &cosmos,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    let err = send_expect_err(
        &mut svm,
        &payer,
        claim_ix(&payer, &beneficiary.pubkey(), &setup, &sig, 600, 0, None),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::ConfigNotFinalized,
    );
}

#[test]
fn claim_against_an_empty_shard_root_is_rejected() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    let cosmos = CosmosKey::from_seed(7);
    let setup = build_single_leaf_setup(&mut svm, &authority, &cosmos, 600, 0);

    let mut empty_shard_cosmos = CosmosKey::from_seed(11);
    let mut address_bytes =
        distributor_core::address::decode_bech32_address(&empty_shard_cosmos.bech32_address())
            .expect("valid address");
    let mut seed = 11u8;
    while distributor_core::shard::shard_id(&address_bytes) == setup.shard_id {
        seed = seed.wrapping_add(1);
        empty_shard_cosmos = CosmosKey::from_seed(seed);
        address_bytes =
            distributor_core::address::decode_bech32_address(&empty_shard_cosmos.bech32_address())
                .expect("valid address");
    }
    let empty_shard_id = distributor_core::shard::shard_id(&address_bytes);

    let forged_setup = SingleLeafSetup {
        shard_id: empty_shard_id,
        leaf_index: 0,
        bech32_address: empty_shard_cosmos.bech32_address(),
        vesting_schedule_hash: distributor_vesting::schedule::ZERO_SCHEDULE_HASH,
        proof: vec![],
        roots_commitment: setup.roots_commitment,
        vault: setup.vault,
        mint: setup.mint,
    };
    let sig = sign_claim(
        &empty_shard_cosmos,
        &beneficiary.pubkey(),
        &forged_setup.roots_commitment,
        &PROGRAM_ID,
    );
    let err = send_expect_err(
        &mut svm,
        &payer,
        claim_ix(
            &payer,
            &beneficiary.pubkey(),
            &forged_setup,
            &sig,
            1,
            0,
            None,
        ),
    );
    assert_distributor_error(&err, distributor::error::DistributorError::InvalidProof);
}

#[test]
fn zero_beneficiary_is_rejected_at_claim() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    let cosmos = CosmosKey::from_seed(7);
    let setup = build_single_leaf_setup(&mut svm, &authority, &cosmos, 600, 0);
    let sig = sign_claim(
        &cosmos,
        &Pubkey::default(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    let err = send_expect_err(
        &mut svm,
        &payer,
        claim_ix(&payer, &Pubkey::default(), &setup, &sig, 600, 0, None),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::InvalidBeneficiary,
    );
}

#[test]
fn claim_past_its_shards_bitmap_capacity_is_rejected() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    let cosmos = CosmosKey::from_seed(7);

    let setup = build_leaf_setup_from_spec(
        &mut svm,
        &authority,
        &cosmos,
        &LeafSetupSpec {
            liquid_amount: 600,
            locked_amount: 0,
            vesting_schedule_hash: distributor_vesting::schedule::ZERO_SCHEDULE_HASH,
            leaf_index: 8,
            bitmap_leaf_count: 1,
            total_claimable: 600,
        },
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
        claim_ix(&payer, &beneficiary.pubkey(), &setup, &sig, 600, 0, None),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::LeafIndexOutOfRange,
    );
}

#[test]
fn claim_folding_past_u64_max_is_rejected() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    let cosmos = CosmosKey::from_seed(7);
    let schedule = continuous_schedule(1, 1_000, 2_000);
    let setup = build_leaf_setup_from_spec(
        &mut svm,
        &authority,
        &cosmos,
        &LeafSetupSpec {
            liquid_amount: u64::MAX,
            locked_amount: 1,
            vesting_schedule_hash: schedule.hash(),
            leaf_index: 0,
            bitmap_leaf_count: 1,
            total_claimable: 1_000,
        },
    );

    set_clock(&mut svm, 3_000);
    let sig = sign_claim(
        &cosmos,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    let err = send_expect_err(
        &mut svm,
        &payer,
        claim_ix(
            &payer,
            &beneficiary.pubkey(),
            &setup,
            &sig,
            u64::MAX,
            1,
            Some(schedule),
        ),
    );
    assert_distributor_error(
        &err,
        distributor::error::DistributorError::ArithmeticOverflow,
    );
}

#[test]
fn revealed_leaf_requires_the_public_key_to_hash_to_its_address() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }

    let attacker = CosmosKey::from_seed(7);

    let mismatched_address = CosmosKey::from_seed(9).bech32_address();
    let address_bytes = distributor_core::address::decode_bech32_address(&mismatched_address)
        .expect("valid address");
    let shard_id = distributor_core::shard::shard_id(&address_bytes);
    let leaf_index = 0u32;
    let liquid_amount = 600u64;
    let locked_amount = 0u64;
    let vesting_schedule_hash = distributor_vesting::schedule::ZERO_SCHEDULE_HASH;

    let leaf_hash = distributor_merkle::leaf_hash(&distributor_merkle::LeafFields {
        shard_id,
        leaf_index,
        compressed_public_key: attacker.compressed_public_key(),
        bech32_address: &mismatched_address,
        liquid_amount,
        locked_amount,
        vesting_schedule_hash,
    });
    let mut roots = vec![
        distributor_core::consts::EMPTY_SHARD_ROOT;
        distributor_core::consts::SHARD_COUNT as usize
    ];
    roots[shard_id as usize] = leaf_hash;
    let mut leaf_counts = vec![0u32; distributor_core::consts::SHARD_COUNT as usize];
    leaf_counts[shard_id as usize] = 1;
    let total_claimable = liquid_amount + locked_amount;
    let roots_commitment =
        distributor_merkle::roots_commitment(&roots, &leaf_counts, total_claimable);

    deploy_upgradeable_distributor(&mut svm, &authority);
    let mint_accounts = mint_and_accounts(
        &mut svm,
        &authority,
        1_000_000,
        total_claimable,
        &PROGRAM_ID,
    );
    send(
        &mut svm,
        &authority,
        initialize_config_ix(&authority, &mint_accounts, total_claimable),
    );
    send(&mut svm, &authority, initialize_roots_ix(&authority));
    for (index, chunk) in roots.chunks(26).enumerate() {
        send(
            &mut svm,
            &authority,
            write_roots_chunk_ix(&authority, u16::try_from(index * 26).unwrap(), chunk),
        );
    }
    for shard in 0..distributor_core::consts::SHARD_COUNT {
        let leaf_count = if shard == shard_id { 1 } else { 0 };
        send(
            &mut svm,
            &authority,
            initialize_bitmap_ix(&authority, shard, leaf_count),
        );
    }
    send(
        &mut svm,
        &authority,
        finalize_genesis_ix(&authority, &mint_accounts, roots_commitment),
    );

    let setup = SingleLeafSetup {
        shard_id,
        leaf_index,
        bech32_address: mismatched_address.clone(),
        vesting_schedule_hash,
        proof: vec![],
        roots_commitment,
        vault: mint_accounts.vault,
        mint: mint_accounts.mint,
    };
    let sig = sign_claim_with_address(
        &attacker,
        &mismatched_address,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    let err = send_expect_err(
        &mut svm,
        &payer,
        claim_ix(
            &payer,
            &beneficiary.pubkey(),
            &setup,
            &sig,
            liquid_amount,
            locked_amount,
            None,
        ),
    );
    assert_distributor_error(&err, distributor::error::DistributorError::IdentityMismatch);
}

#[test]
fn high_s_twin_of_a_valid_signature_is_rejected() {
    let mut svm = LiteSVM::new();
    let authority = Keypair::new();
    let payer = Keypair::new();
    let beneficiary = Keypair::new();
    for signer in [&authority, &payer] {
        svm.airdrop(&signer.pubkey(), 100_000_000_000).unwrap();
    }
    let cosmos = CosmosKey::from_seed(7);
    let setup = build_single_leaf_setup(&mut svm, &authority, &cosmos, 600, 0);

    let high = sign_claim_high_s(
        &cosmos,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    let err = send_expect_err(
        &mut svm,
        &payer,
        claim_ix(&payer, &beneficiary.pubkey(), &setup, &high, 600, 0, None),
    );
    assert_distributor_error(&err, distributor::error::DistributorError::InvalidSignature);

    let low = sign_claim(
        &cosmos,
        &beneficiary.pubkey(),
        &setup.roots_commitment,
        &PROGRAM_ID,
    );
    send(
        &mut svm,
        &payer,
        claim_ix(&payer, &beneficiary.pubkey(), &setup, &low, 600, 0, None),
    );
    assert_eq!(
        read_token_amount(&svm, &setup.beneficiary_token(&beneficiary.pubkey())),
        600
    );
}
