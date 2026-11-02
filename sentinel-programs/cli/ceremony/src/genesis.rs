use anyhow::Context;
use solana_client::rpc_client::RpcClient;
use solana_pubkey::Pubkey;

pub const MAINNET_GENESIS_HASH: &str = "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d";

pub const DEVNET_GENESIS_HASH: &str = "EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG";

pub const TESTNET_GENESIS_HASH: &str = "4uhcVJyU9pJkvQyS88uRDiswHXSCkY3zQawwpjk2NsNY";

const KNOWN_CLUSTERS: &[(&str, &str)] = &[
    ("mainnet-beta", MAINNET_GENESIS_HASH),
    ("devnet", DEVNET_GENESIS_HASH),
    ("testnet", TESTNET_GENESIS_HASH),
];

#[must_use]
pub fn known_cluster_name(genesis_hash: &str) -> Option<&'static str> {
    KNOWN_CLUSTERS
        .iter()
        .find(|(_, known_hash)| *known_hash == genesis_hash)
        .map(|(name, _)| *name)
}

pub fn check_genesis_hash(
    source_label: &str,
    fetched_hash: &str,
    expected_name: &str,
    expected_hash: &str,
    allow_unknown: bool,
) -> anyhow::Result<()> {
    if fetched_hash == expected_hash {
        return Ok(());
    }
    match known_cluster_name(fetched_hash) {
        Some(actual_name) => anyhow::bail!(
            "{source_label} genesis hash {fetched_hash} is {actual_name}'s, not \
             {expected_name}'s ({expected_hash}) -- refusing to run the irreversible ceremony \
             against the wrong cluster (this check is never overridable)"
        ),
        None if allow_unknown => {
            eprintln!(
                "WARNING: --allow-unknown-genesis set: {source_label} genesis hash \
                 {fetched_hash} does not match any known cluster (expected {expected_name} \
                 {expected_hash}); proceeding anyway because the operator explicitly opted in"
            );
            Ok(())
        }
        None => anyhow::bail!(
            "{source_label} genesis hash {fetched_hash} does not match any known cluster \
             (expected {expected_name} {expected_hash}); pass --allow-unknown-genesis to \
             proceed against an unrecognized/local cluster"
        ),
    }
}

pub fn require_genesis_matches(
    client: &RpcClient,
    source_label: &str,
    expected_name: &str,
    expected_hash: &str,
    allow_unknown: bool,
) -> anyhow::Result<()> {
    let fetched = client
        .get_genesis_hash()
        .with_context(|| format!("failed to fetch genesis hash for {source_label}"))?
        .to_string();
    check_genesis_hash(
        source_label,
        &fetched,
        expected_name,
        expected_hash,
        allow_unknown,
    )
}

pub fn require_program_id_not_on_other_cluster(
    program_id: &Pubkey,
    other_cluster_client: &RpcClient,
) -> anyhow::Result<()> {
    use solana_commitment_config::CommitmentConfig;
    match other_cluster_client
        .get_account_with_commitment(program_id, CommitmentConfig::finalized())
    {
        Ok(resp) if resp.value.is_some() => anyhow::bail!(
            "program id {program_id} also exists on the other cluster — ids must be distinct"
        ),
        Ok(_) => Ok(()),
        Err(error) => Err(error).context("could not verify the other cluster (failing closed)"),
    }
}

#[cfg(test)]
#[allow(clippy::unwrap_used, clippy::expect_used)]
mod tests {
    use super::*;

    #[test]
    fn check_genesis_hash_passes_on_exact_match() {
        check_genesis_hash(
            "--rpc-url",
            MAINNET_GENESIS_HASH,
            "mainnet-beta",
            MAINNET_GENESIS_HASH,
            false,
        )
        .unwrap();
    }

    #[test]
    fn check_genesis_hash_hard_fails_on_a_known_but_wrong_cluster_even_with_allow_unknown() {
        for allow_unknown in [false, true] {
            let err = check_genesis_hash(
                "--rpc-url",
                DEVNET_GENESIS_HASH,
                "mainnet-beta",
                MAINNET_GENESIS_HASH,
                allow_unknown,
            )
            .unwrap_err()
            .to_string();
            assert!(err.contains("devnet"), "got: {err}");
            assert!(err.contains("mainnet-beta"), "got: {err}");
            assert!(err.contains("never overridable"), "got: {err}");
        }
    }

    #[test]
    fn check_genesis_hash_unknown_cluster_requires_the_explicit_flag() {
        let refused = check_genesis_hash(
            "--rpc-url",
            "some localnet hash",
            "mainnet-beta",
            MAINNET_GENESIS_HASH,
            false,
        )
        .unwrap_err()
        .to_string();
        assert!(
            refused.contains("--allow-unknown-genesis"),
            "got: {refused}"
        );

        check_genesis_hash(
            "--rpc-url",
            "some localnet hash",
            "mainnet-beta",
            MAINNET_GENESIS_HASH,
            true,
        )
        .expect("explicit --allow-unknown-genesis permits an unrecognized cluster");
    }
}
