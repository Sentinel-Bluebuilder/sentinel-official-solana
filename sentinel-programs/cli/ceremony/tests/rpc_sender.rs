use ceremony::CeremonyError;
use ceremony::runner::{CeremonySigners, run_steps};
use ceremony::sender::{AccountReader, ROOTS_PER_CHUNK, RpcSender, Sender, Transport};
use ceremony::steps::{
    CeremonyAddresses, CeremonyInputs, RentTable, build_ceremony_steps, build_finalize_genesis_step,
};
use litesvm::LiteSVM;
use solana_sdk::{
    hash::Hash,
    pubkey::Pubkey,
    signature::{Keypair, Signature},
    signer::Signer,
    transaction::VersionedTransaction,
};
use std::sync::Arc;

struct SvmTransport<'a>(&'a mut LiteSVM);

impl AccountReader for SvmTransport<'_> {
    fn account_exists(&self, address: &Pubkey) -> bool {
        self.0.get_account(address).is_some()
    }
    fn account_data(&self, address: &Pubkey) -> Option<Vec<u8>> {
        self.0.get_account(address).map(|account| account.data)
    }
}

impl Transport for SvmTransport<'_> {
    fn latest_blockhash(&self) -> Result<Hash, CeremonyError> {
        Ok(self.0.latest_blockhash())
    }
    fn submit(&mut self, transaction: &VersionedTransaction) -> Result<Signature, CeremonyError> {
        let signature = transaction.signatures.first().copied().unwrap_or_default();
        self.0
            .send_transaction(transaction.clone())
            .map(|meta| meta.signature)
            .map_err(|error| CeremonyError::TransportFailed {
                signature,
                message: format!("transaction failed: {error:?}"),
            })
    }
}

struct Fixture {
    svm: LiteSVM,
    inputs: CeremonyInputs,
    keys: CeremonyAddresses,
    rent: RentTable,
    signers: CeremonySigners,
}

fn fixture() -> Fixture {
    let mut svm = LiteSVM::new();
    let upgrade_authority = Keypair::new();
    svm.airdrop(&upgrade_authority.pubkey(), 1_000_000_000_000)
        .unwrap();
    let program_id =
        distributor_test_support::deploy_upgradeable_distributor(&mut svm, &upgrade_authority);

    let payer = Arc::new(Keypair::new());
    svm.airdrop(&payer.pubkey(), 1_000_000_000_000).unwrap();

    let cosmos = distributor_test_support::CosmosKey::from_seed(7);
    let tree = distributor_test_support::synth_single_leaf_tree(&cosmos, 600, 0);
    let total_claimable = distributor_test_support::recount_total_claimable(&tree);

    let mint_keypair = Keypair::new();
    let treasury_keypair = Keypair::new();
    let vault_keypair = Keypair::new();
    let keys = CeremonyAddresses {
        upgrade_authority: upgrade_authority.pubkey(),
        payer: payer.pubkey(),
        mint: mint_keypair.pubkey(),
        treasury: treasury_keypair.pubkey(),
        vault: vault_keypair.pubkey(),
    };
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
        treasury_amount: 400,
        expected_roots_commitment: distributor_test_support::roots_commitment_of(&tree),
    };
    let rent = RentTable {
        mint_at_create_lamports: 5_000_000,
        mint_with_metadata_lamports: 6_000_000,
        token_account_lamports: 3_000_000,
    };
    let signers = CeremonySigners {
        upgrade_authority: Some(upgrade_authority),
        payer,
        mint: mint_keypair,
        treasury: treasury_keypair,
        vault: vault_keypair,
    };
    Fixture {
        svm,
        inputs,
        keys,
        rent,
        signers,
    }
}

#[test]
fn rpc_sender_runs_the_whole_ceremony_with_a_payer_distinct_from_the_upgrade_authority() {
    let Fixture {
        mut svm,
        inputs,
        keys,
        rent,
        signers,
    } = fixture();
    assert_ne!(
        keys.payer, keys.upgrade_authority,
        "this test only means anything with a distinct payer"
    );

    let mut steps = build_ceremony_steps(&inputs, &keys, &rent, ROOTS_PER_CHUNK);
    steps.push(build_finalize_genesis_step(&inputs, &keys));

    {
        let mut sender = RpcSender::new(
            SvmTransport(&mut svm),
            &signers.payer,
            signers.upgrade_authority.as_ref(),
            1,
        );
        let records = run_steps(&mut sender, &steps, &signers)
            .unwrap_or_else(|error| panic!("production RpcSender must run every step: {error}"));

        assert_eq!(records.len(), steps.len());
        for (record, step) in records.iter().zip(&steps) {
            assert_eq!(record.step, step.name);
            assert!(
                !record.skipped,
                "{} unexpectedly reported skipped",
                step.name
            );
            assert!(
                record
                    .signature
                    .is_some_and(|signature| signature != Signature::default()),
                "{} did not carry a real signature",
                step.name
            );
        }
    }

    let cfg = distributor_test_support::read_config(&svm);
    assert!(cfg.finalized);
    assert_eq!(cfg.total_claimable, 600);
    assert_eq!(cfg.bitmap_count, distributor_core::consts::SHARD_COUNT);
}

#[test]
fn rpc_sender_resumes_genesis_through_the_shared_predicates() {
    let Fixture {
        mut svm,
        inputs,
        keys,
        rent,
        signers,
    } = fixture();
    let genesis = build_ceremony_steps(&inputs, &keys, &rent, ROOTS_PER_CHUNK);

    {
        let mut sender = RpcSender::new(
            SvmTransport(&mut svm),
            &signers.payer,
            signers.upgrade_authority.as_ref(),
            1,
        );
        run_steps(&mut sender, &genesis, &signers).expect("first genesis pass");

        for name in [
            "distributor_create_mint",
            "distributor_initialize_metadata",
            "distributor_create_treasury",
            "distributor_create_vault",
            "distributor_mint_supply",
            "distributor_initialize_config",
            "distributor_initialize_roots",
            "distributor_initialize_bitmap_0",
            "distributor_initialize_bitmap_127",
        ] {
            let step = genesis
                .iter()
                .find(|step| step.name == name)
                .unwrap_or_else(|| panic!("no step {name}"));
            assert!(
                sender.is_step_done(step),
                "{name} landed but the resume predicate does not see it"
            );
        }
    }

    {
        let mut sender = RpcSender::new(
            SvmTransport(&mut svm),
            &signers.payer,
            signers.upgrade_authority.as_ref(),
            1,
        );
        let resumed = run_steps(&mut sender, &genesis, &signers).expect("resumed genesis pass");

        for name in [
            "distributor_create_mint",
            "distributor_initialize_metadata",
            "distributor_create_treasury",
            "distributor_create_vault",
            "distributor_mint_supply",
            "distributor_initialize_config",
            "distributor_initialize_roots",
            "distributor_initialize_bitmap_0",
            "distributor_initialize_bitmap_127",
        ] {
            let record = resumed
                .iter()
                .find(|record| record.step == name)
                .unwrap_or_else(|| panic!("no record for {name}"));
            assert!(record.skipped, "{name} unexpectedly resent");
            assert!(
                record.signature.is_none(),
                "{name} unexpectedly has a signature"
            );
        }
        let finalize_genesis = build_finalize_genesis_step(&inputs, &keys);
        let finalized = run_steps(
            &mut sender,
            std::slice::from_ref(&finalize_genesis),
            &signers,
        )
        .expect("finalize genesis");
        assert!(!finalized[0].skipped);
        assert!(
            finalized[0]
                .signature
                .is_some_and(|signature| signature != Signature::default())
        );
    }

    let cfg = distributor_test_support::read_config(&svm);
    assert!(cfg.finalized);
    assert_eq!(cfg.bitmap_count, distributor_core::consts::SHARD_COUNT);
}
