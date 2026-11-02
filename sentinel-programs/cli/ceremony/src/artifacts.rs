use anyhow::Context;
use distributor_dataset::Attestation;
use serde::Deserialize;

#[derive(Deserialize)]
pub struct Reconciliation {
    pub supply_at_halt: String,
    pub total_claimable: String,
    pub treasury_total: String,
    pub ledger_content_hash: String,
}

pub struct DerivedSupply {
    pub vault_amount: u64,
    pub treasury_amount: u64,
    pub expected_roots_commitment: [u8; 32],
}

pub fn derive_supply(
    attestation: &Attestation,
    reconciliation: &Reconciliation,
) -> anyhow::Result<DerivedSupply> {
    let supply_at_halt: u64 = reconciliation
        .supply_at_halt
        .parse()
        .context("supply_at_halt")?;
    let total_claimable: u64 = reconciliation
        .total_claimable
        .parse()
        .context("total_claimable")?;
    let treasury_total: u64 = reconciliation
        .treasury_total
        .parse()
        .context("treasury_total")?;
    if attestation.shard_count != distributor_core::consts::SHARD_COUNT {
        anyhow::bail!(
            "attestation shard_count={} but this build is SHARD_COUNT={}",
            attestation.shard_count,
            distributor_core::consts::SHARD_COUNT
        );
    }
    if attestation.total_claimable != total_claimable {
        anyhow::bail!(
            "total_claimable disagrees: attestation {} vs reconciliation {}",
            attestation.total_claimable,
            total_claimable
        );
    }

    if distributor_dataset::decode_hex_array::<32>(&attestation.ledger_content_hash)?
        != distributor_dataset::decode_hex_array::<32>(&reconciliation.ledger_content_hash)?
    {
        anyhow::bail!("ledger content hash mismatch between attestation and reconciliation");
    }
    if supply_at_halt.checked_sub(total_claimable) != Some(treasury_total) {
        anyhow::bail!(
            "treasury_total {treasury_total} != supply_at_halt {supply_at_halt} - total_claimable {total_claimable}"
        );
    }

    let expected_roots_commitment =
        distributor_dataset::decode_hex_array::<32>(&attestation.roots_commitment)?;
    Ok(DerivedSupply {
        vault_amount: total_claimable,
        treasury_amount: treasury_total,
        expected_roots_commitment,
    })
}
