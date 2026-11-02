use solana_sdk::{signature::Keypair, signer::Signer};
use std::sync::Arc;
use xtask::rehearse::run_distributor_in_svm;

#[test]
fn synth_to_claims_full_pipeline() {
    let mut svm = test_support::build_litesvm();
    let upgrade_authority = Arc::new(Keypair::new());
    svm.airdrop(&upgrade_authority.pubkey(), 10_000_000_000_000)
        .unwrap();

    let report = run_distributor_in_svm(&mut svm, upgrade_authority, 40);

    assert!(report.finalized);
    assert_eq!(report.bitmap_count, distributor_core::consts::SHARD_COUNT);
    assert_eq!(
        report.vault_after + report.total_claimed,
        report.vault_before
    );
    assert_eq!(report.vault_before, report.total_claimable);
    assert!(report.liquid_claims > 0 && report.unrevealed_claims > 0 && report.locked_claims > 0);
    assert!(report.withdrawn_amount > 0);
    assert!(report.double_claim_rejected);
    assert!(report.build_self_verify_passed);

    assert!(report.bitmap_capacity_ok);

    assert!(report.build_seconds > 0.0 && report.ceremony_seconds > 0.0);
}

#[test]
fn dvpn_rehearsal_exercises_hot_paths_in_svm() {
    let mut svm = test_support::build_litesvm();
    let upgrade_authority = Keypair::new();
    svm.airdrop(&upgrade_authority.pubkey(), 1_000_000_000_000)
        .unwrap();
    let report = xtask::rehearse::run_dvpn_in_svm(&mut svm, &upgrade_authority).unwrap();
    assert!(report.grantee_channel_funded_via_delegate);
    assert!(report.grantee_lease_funded_via_delegate);
    assert!(report.redeemed_channels_in_one_v1_tx >= 2);
    assert!(report.stream_channel_opened_redeemed_closed_by_grantee);
    assert!(report.node_heartbeat_by_grantee);
    assert!(report.batched_fee_sweep);
    assert!(report.payer_was_fee_payer_and_rent_payer);
}
