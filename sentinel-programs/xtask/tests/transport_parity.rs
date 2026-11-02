use ceremony::sender::{MAX_CU_LIMIT, MAX_LOADED_ACCOUNTS_DATA_BYTES, ROOTS_PER_CHUNK};
use ceremony::steps::{CeremonyAddresses, CeremonyInputs, RentTable, build_ceremony_steps};
use ceremony::wire_size::check_tx_within_limit;
use litesvm::LiteSVM;
use solana_sdk::{pubkey::Pubkey, signature::Keypair, signer::Signer};
use std::sync::Arc;
use txv1::{TxConfig, priority_fee_lamports};
use xtask::rehearse::{
    DistributorRehearsalReport, REHEARSAL_PRIORITY_FEE_MICRO_LAMPORTS_PER_CU,
    run_distributor_in_svm,
};

fn rehearse() -> (DistributorRehearsalReport, Pubkey) {
    let mut svm = LiteSVM::new();
    let upgrade_authority = Arc::new(Keypair::new());
    svm.airdrop(&upgrade_authority.pubkey(), 10_000_000_000_000)
        .unwrap();
    let upgrade_authority_public_key = upgrade_authority.pubkey();
    let report = run_distributor_in_svm(&mut svm, upgrade_authority, 4);
    (report, upgrade_authority_public_key)
}

#[test]
fn rehearsal_transport_matches_production() {
    let (report, upgrade_authority) = rehearse();

    assert_eq!(
        report.roots_per_chunk, ROOTS_PER_CHUNK,
        "the rehearsal must chunk roots with production's ROOTS_PER_CHUNK"
    );
    assert_ne!(
        report.roots_per_chunk, 26,
        "26 was the rehearsal-local literal this finding removed"
    );
    let expected_chunks =
        (distributor_core::consts::SHARD_COUNT as usize).div_ceil(ROOTS_PER_CHUNK);
    assert_eq!(
        report.roots_chunk_steps,
        expected_chunks,
        "SHARD_COUNT={} at {ROOTS_PER_CHUNK}/chunk is {expected_chunks} write_roots_chunk steps",
        distributor_core::consts::SHARD_COUNT
    );

    assert_eq!(report.upgrade_authority, upgrade_authority);
    assert_ne!(
        report.payer, report.upgrade_authority,
        "production signs every authority-bearing step with a payer distinct from \
         the authority; a rehearsal with payer == authority validates a narrower \
         transaction than the launch sends"
    );

    assert_eq!(
        report.priority_fee_micro_lamports_per_cu,
        REHEARSAL_PRIORITY_FEE_MICRO_LAMPORTS_PER_CU
    );
    assert!(
        report.priority_fee_micro_lamports_per_cu > 0,
        "a zero priority fee would leave the v1 header's fee field absent on every \
         rehearsal transaction and stop validating the launch wire budget"
    );

    let total_claimable = report.total_claimable;
    assert_eq!(
        report.treasury_amount,
        total_claimable + 1,
        "treasury must be supply_at_halt - Σ leaves from production's derive_supply"
    );
    assert_ne!(
        report.treasury_amount,
        total_claimable / 4,
        "Σ/4 was the hardcoded rehearsal split this finding removed"
    );

    assert!(report.finalized && report.double_claim_rejected);
}

#[test]
fn rehearsal_chunk_size_is_at_the_production_wire_bound() {
    let payer = Pubkey::new_unique();
    let upgrade_authority = Pubkey::new_unique();
    let cfg =
        TxConfig::new(MAX_CU_LIMIT, MAX_LOADED_ACCOUNTS_DATA_BYTES).with_priority_fee_lamports(
            priority_fee_lamports(REHEARSAL_PRIORITY_FEE_MICRO_LAMPORTS_PER_CU, MAX_CU_LIMIT),
        );

    let chunk_step = |roots_per_chunk: usize| {
        let inputs = CeremonyInputs {
            program_id: Pubkey::new_unique(),
            token_metadata: distributor_mint::create::TokenMetadataParams {
                name: "Sentinel".into(),
                symbol: "DVPN".into(),
                uri: "https://sentinel.co/metadata.json".into(),
            },
            roots: (0..roots_per_chunk)
                .map(|index| [u8::try_from(index).expect("roots_per_chunk fits u8") + 1; 32])
                .collect(),
            shard_leaf_counts: vec![],
            vault_amount: 1,
            treasury_amount: 1,
            expected_roots_commitment: [0u8; 32],
        };
        let keys = CeremonyAddresses {
            upgrade_authority,
            payer,
            mint: Pubkey::new_unique(),
            treasury: Pubkey::new_unique(),
            vault: Pubkey::new_unique(),
        };
        let rent = RentTable {
            mint_at_create_lamports: 1,
            mint_with_metadata_lamports: 2,
            token_account_lamports: 3,
        };
        build_ceremony_steps(&inputs, &keys, &rent, roots_per_chunk)
            .into_iter()
            .find(|step| step.name == "distributor_write_roots_chunk_0")
            .expect("a roots chunk step")
    };

    let fits = chunk_step(ROOTS_PER_CHUNK);
    assert!(
        check_tx_within_limit(&fits.ixs, &payer, &cfg).is_ok(),
        "ROOTS_PER_CHUNK={ROOTS_PER_CHUNK} must fit the rehearsal's \
         (== production's) transaction"
    );

    let over = chunk_step(ROOTS_PER_CHUNK + 1);
    assert!(
        check_tx_within_limit(&over.ixs, &payer, &cfg).is_err(),
        "ROOTS_PER_CHUNK+1 unexpectedly still fits -- the shared cap is not tight"
    );
}
