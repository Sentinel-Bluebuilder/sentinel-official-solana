use ceremony::CeremonyError;
use ceremony::sender::{MAX_CU_LIMIT, MAX_LOADED_ACCOUNTS_DATA_BYTES, Sender};
use ceremony::steps::{
    CeremonyAddresses, CeremonyInputs, RentTable, build_ceremony_steps, build_finalize_genesis_step,
};
use ceremony::wire_size::check_tx_within_limit;
use litesvm::LiteSVM;
use solana_sdk::{
    instruction::Instruction,
    signature::{Keypair, Signature},
    signer::Signer,
    transaction::Transaction,
};

struct LiteSender<'a> {
    svm: &'a mut LiteSVM,
    payer: &'a Keypair,
}

impl<'a> Sender for LiteSender<'a> {
    fn send(
        &mut self,
        _name: &str,
        ixs: &[Instruction],
        signers: &[&Keypair],
    ) -> Result<Signature, CeremonyError> {
        let blockhash = self.svm.latest_blockhash();
        let mut all_signers = vec![self.payer];
        all_signers.extend_from_slice(signers);
        let transaction = Transaction::new_signed_with_payer(
            ixs,
            Some(&self.payer.pubkey()),
            &all_signers,
            blockhash,
        );
        let signature = transaction.signatures.first().copied().unwrap_or_default();
        self.svm
            .send_transaction(transaction)
            .map(|meta| meta.signature)
            .map_err(|error| CeremonyError::TransportFailed {
                signature,
                message: format!("{error:?}"),
            })
    }
    fn is_step_done(&self, _step: &ceremony::steps::Step) -> bool {
        false
    }
}

#[test]
fn full_ceremony_finalizes_genesis_and_a_claim_succeeds() {
    let mut svm = LiteSVM::new();
    let upgrade_authority = Keypair::new();
    svm.airdrop(&upgrade_authority.pubkey(), 1_000_000_000_000)
        .unwrap();
    let program_id =
        distributor_test_support::deploy_upgradeable_distributor(&mut svm, &upgrade_authority);

    let cosmos_key = distributor_test_support::CosmosKey::from_seed(7);
    let tree = distributor_test_support::synth_single_leaf_tree(&cosmos_key, 600, 0);
    let total_claimable = distributor_test_support::recount_total_claimable(&tree);
    let supply_at_halt = total_claimable + 400;

    let mint_keypair = Keypair::new();
    let treasury_keypair = Keypair::new();
    let vault_keypair = Keypair::new();
    let keys = CeremonyAddresses {
        upgrade_authority: upgrade_authority.pubkey(),
        payer: upgrade_authority.pubkey(),
        mint: mint_keypair.pubkey(),
        treasury: treasury_keypair.pubkey(),
        vault: vault_keypair.pubkey(),
    };
    let signer_candidates: [&Keypair; 4] = [
        &upgrade_authority,
        &mint_keypair,
        &treasury_keypair,
        &vault_keypair,
    ];
    let inputs = CeremonyInputs {
        program_id,
        token_metadata: distributor_mint::create::TokenMetadataParams {
            name: "Sentinel".into(),
            symbol: "DVPN".into(),
            uri: "https://sentinel.co/metadata.json".into(),
        },
        roots: tree.roots.clone(),
        shard_leaf_counts: distributor_test_support::recount_shard_leaf_counts(&tree),
        vault_amount: total_claimable,
        treasury_amount: supply_at_halt - total_claimable,
        expected_roots_commitment: distributor_test_support::roots_commitment_of(&tree),
    };
    let rent = RentTable {
        mint_at_create_lamports: 5_000_000,
        mint_with_metadata_lamports: 6_000_000,
        token_account_lamports: 3_000_000,
    };

    let mut steps = build_ceremony_steps(&inputs, &keys, &rent, 26);
    steps.push(build_finalize_genesis_step(&inputs, &keys));

    let tx_config = txv1::TxConfig::new(MAX_CU_LIMIT, MAX_LOADED_ACCOUNTS_DATA_BYTES)
        .with_priority_fee_lamports(1);
    for step in &steps {
        check_tx_within_limit(&step.ixs, &upgrade_authority.pubkey(), &tx_config).unwrap_or_else(
            |error| panic!("step {} exceeds the v1 wire limit: {error}", step.name),
        );
    }

    let mut sender = LiteSender {
        svm: &mut svm,
        payer: &upgrade_authority,
    };
    for step in &steps {
        let signer_kps =
            distributor_test_support::resolve_signers(&signer_candidates, &step.extra_signers);
        sender
            .send(&step.name, &step.ixs, &signer_kps)
            .unwrap_or_else(|error| panic!("step {} failed: {error}", step.name));
    }

    let config = distributor_test_support::read_config(&svm);
    assert!(config.finalized);
    assert_eq!(config.total_claimable, 600);
    assert_eq!(config.bitmap_count, distributor_core::consts::SHARD_COUNT);

    let beneficiary = Keypair::new();
    let signature = distributor_test_support::sign_claim(
        &cosmos_key,
        &beneficiary.pubkey(),
        &config.roots_commitment,
        &program_id,
    );
    distributor_test_support::send(
        &mut svm,
        &upgrade_authority,
        distributor_test_support::claim_first_leaf_ix_with_program_id(
            &program_id,
            &keys,
            &tree,
            &upgrade_authority,
            &beneficiary.pubkey(),
            &signature,
            600,
            0,
        ),
    );
    assert_eq!(
        distributor_test_support::read_token_amount(
            &svm,
            &distributor_test_support::ata(&beneficiary.pubkey(), &keys.mint)
        ),
        600
    );
}

#[test]
fn ceremony_halts_when_the_roots_commitment_gate_mismatches() {
    let mut svm = LiteSVM::new();
    let upgrade_authority = Keypair::new();
    svm.airdrop(&upgrade_authority.pubkey(), 1_000_000_000_000)
        .unwrap();
    let program_id =
        distributor_test_support::deploy_upgradeable_distributor(&mut svm, &upgrade_authority);
    let cosmos_key = distributor_test_support::CosmosKey::from_seed(7);
    let tree = distributor_test_support::synth_single_leaf_tree(&cosmos_key, 600, 0);
    let total_claimable = distributor_test_support::recount_total_claimable(&tree);
    let mint_keypair = Keypair::new();
    let treasury_keypair = Keypair::new();
    let vault_keypair = Keypair::new();
    let keys = CeremonyAddresses {
        upgrade_authority: upgrade_authority.pubkey(),
        payer: upgrade_authority.pubkey(),
        mint: mint_keypair.pubkey(),
        treasury: treasury_keypair.pubkey(),
        vault: vault_keypair.pubkey(),
    };
    let signer_candidates: [&Keypair; 4] = [
        &upgrade_authority,
        &mint_keypair,
        &treasury_keypair,
        &vault_keypair,
    ];
    let inputs = CeremonyInputs {
        program_id,
        token_metadata: distributor_mint::create::TokenMetadataParams {
            name: "S".into(),
            symbol: "D".into(),
            uri: "https://sentinel.co/metadata.json".into(),
        },
        roots: tree.roots.clone(),
        shard_leaf_counts: distributor_test_support::recount_shard_leaf_counts(&tree),
        vault_amount: total_claimable,
        treasury_amount: 400,
        expected_roots_commitment: [0xff; 32],
    };
    let rent = RentTable {
        mint_at_create_lamports: 5_000_000,
        mint_with_metadata_lamports: 6_000_000,
        token_account_lamports: 3_000_000,
    };
    let mut steps = build_ceremony_steps(&inputs, &keys, &rent, 26);
    steps.push(build_finalize_genesis_step(&inputs, &keys));
    let mut sender = LiteSender {
        svm: &mut svm,
        payer: &upgrade_authority,
    };
    let mut halted = false;
    for step in &steps {
        let signer_kps =
            distributor_test_support::resolve_signers(&signer_candidates, &step.extra_signers);
        if sender.send(&step.name, &step.ixs, &signer_kps).is_err() {
            halted = true;
            break;
        }
    }
    assert!(
        halted,
        "ceremony must halt on the failed finalize genesis step, not proceed"
    );
}
