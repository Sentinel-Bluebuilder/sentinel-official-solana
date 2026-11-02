use ceremony::artifacts::{Reconciliation, derive_supply};
use distributor_dataset::Attestation;

const ROOTS_COMMITMENT_HEX: &str =
    "aa00000000000000000000000000000000000000000000000000000000000000";
const LEDGER_CONTENT_HASH_HEX: &str =
    "cc00000000000000000000000000000000000000000000000000000000000000";

fn attestation_with_total(total_claimable: u64) -> Attestation {
    Attestation {
        schema_version: distributor_dataset::ATTESTATION_SCHEMA_VERSION,
        roots_commitment: ROOTS_COMMITMENT_HEX.into(),
        shard_leaf_counts: {
            let mut counts = vec![0u32; distributor_core::consts::SHARD_COUNT as usize];
            counts[0] = 1;
            counts
        },
        total_claimable,
        leaf_count: 1,
        shard_count: 128,
        ledger_content_hash: LEDGER_CONTENT_HASH_HEX.into(),
        claim_dataset_manifest_hash: "00".into(),
        git_commit: "g".into(),
        tool_version: "t".into(),
    }
}

#[test]
fn derive_supply_cross_checks_artifacts() {
    let reconciliation = Reconciliation {
        supply_at_halt: "1000".into(),
        total_claimable: "600".into(),
        treasury_total: "400".into(),
        ledger_content_hash: LEDGER_CONTENT_HASH_HEX.into(),
    };
    let derived = derive_supply(&attestation_with_total(600), &reconciliation).unwrap();
    assert_eq!(derived.vault_amount, 600);
    assert_eq!(derived.treasury_amount, 400);
    assert_eq!(
        hex::encode(derived.expected_roots_commitment),
        ROOTS_COMMITMENT_HEX
    );
}

#[test]
fn derive_supply_rejects_treasury_total_that_breaks_the_supply_balance() {
    let reconciliation = Reconciliation {
        supply_at_halt: "1000".into(),
        total_claimable: "600".into(),
        treasury_total: "399".into(),
        ledger_content_hash: LEDGER_CONTENT_HASH_HEX.into(),
    };
    assert!(derive_supply(&attestation_with_total(600), &reconciliation).is_err());
}

#[test]
fn derive_supply_rejects_ledger_hash_mismatch() {
    let reconciliation = Reconciliation {
        supply_at_halt: "1000".into(),
        total_claimable: "600".into(),
        treasury_total: "400".into(),
        ledger_content_hash: "dd00000000000000000000000000000000000000000000000000000000000000"
            .into(),
    };
    assert!(derive_supply(&attestation_with_total(600), &reconciliation).is_err());
}
