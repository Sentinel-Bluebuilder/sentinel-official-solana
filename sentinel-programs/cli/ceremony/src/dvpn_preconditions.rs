use anchor_lang::AccountDeserialize;
use solana_feature_gate_interface::state::Feature;
use solana_pubkey::{Pubkey, pubkey};

use crate::sender::AccountReader;

pub const SHA512_FEATURE_ID: Pubkey = pubkey!("s512oDwgx8hjMnaQjXfqqrZroVj4HvC6TkN3iSSWXCh");

pub fn require_sha512_feature_active(reader: &dyn AccountReader) -> anyhow::Result<()> {
    let data = reader
        .account_data(&SHA512_FEATURE_ID)
        .ok_or_else(|| anyhow::anyhow!("SHA-512 feature account absent; unpause refused"))?;
    let feature: Feature = bincode::deserialize(&data)
        .map_err(|error| anyhow::anyhow!("SHA-512 feature account unparseable: {error}"))?;
    anyhow::ensure!(
        feature.activated_at.is_some(),
        "SHA-512 feature not active on this cluster; unpause refused"
    );
    Ok(())
}

pub fn require_dvpn_config_matches(
    reader: &dyn AccountReader,
    config: &Pubkey,
    expected_upgrade_authority: &Pubkey,
    expected_vault: &Pubkey,
    expected_mint: &Pubkey,
    expected_treasury: &Pubkey,
) -> anyhow::Result<()> {
    let data = reader
        .account_data(config)
        .ok_or_else(|| anyhow::anyhow!("dvpn Config account not found; unpause refused"))?;
    let mut slice: &[u8] = &data;
    let cfg = dvpn::state::Config::try_deserialize(&mut slice)
        .map_err(|error| anyhow::anyhow!("dvpn Config account does not deserialize: {error}"))?;
    anyhow::ensure!(
        cfg.authority == *expected_upgrade_authority,
        "dvpn Config authority {} does not match the expected upgrade authority \
         {expected_upgrade_authority}; unpause refused",
        cfg.authority
    );
    anyhow::ensure!(
        cfg.vault == *expected_vault,
        "dvpn Config vault {} does not match the expected vault {expected_vault}; unpause refused",
        cfg.vault
    );
    anyhow::ensure!(
        cfg.mint == *expected_mint,
        "dvpn Config mint {} does not match the expected mint {expected_mint}; unpause refused",
        cfg.mint
    );
    anyhow::ensure!(
        cfg.treasury == *expected_treasury,
        "dvpn Config treasury {} does not match the expected treasury {expected_treasury}; \
         unpause refused",
        cfg.treasury
    );
    anyhow::ensure!(
        cfg.paused_mask == dvpn_core::constants::PAUSE_MASK,
        "dvpn Config paused_mask byte is {:#04x}, not fully paused ({:#04x}); refusing to unpause \
         (the is_dvpn_config_unpaused resume-skip should have caught this)",
        cfg.paused_mask,
        dvpn_core::constants::PAUSE_MASK
    );
    Ok(())
}

#[cfg(test)]
#[allow(clippy::unwrap_used, clippy::panic, clippy::indexing_slicing)]
mod tests {
    use super::*;
    use std::collections::HashMap;

    struct FakeAccountReader(HashMap<Pubkey, Vec<u8>>);
    impl AccountReader for FakeAccountReader {
        fn account_exists(&self, address: &Pubkey) -> bool {
            self.0.contains_key(address)
        }
        fn account_data(&self, address: &Pubkey) -> Option<Vec<u8>> {
            self.0.get(address).cloned()
        }
    }

    fn feature_bytes(activated_at: Option<u64>) -> Vec<u8> {
        bincode::serialize(&Feature { activated_at }).unwrap()
    }

    #[test]
    fn require_sha512_feature_active_refuses_not_yet_activated() {
        let mut accounts = HashMap::new();
        accounts.insert(SHA512_FEATURE_ID, feature_bytes(None));
        let reader = FakeAccountReader(accounts);
        let err = require_sha512_feature_active(&reader).unwrap_err();
        assert!(err.to_string().contains("not active"));
    }

    #[test]
    fn require_sha512_feature_active_passes_when_activated() {
        let mut accounts = HashMap::new();
        accounts.insert(SHA512_FEATURE_ID, feature_bytes(Some(12345)));
        let reader = FakeAccountReader(accounts);
        assert!(require_sha512_feature_active(&reader).is_ok());
    }

    #[allow(clippy::too_many_arguments)]
    fn fabricate_config(
        upgrade_authority: Pubkey,
        vault: Pubkey,
        mint: Pubkey,
        treasury: Pubkey,
        paused_mask: u8,
    ) -> Vec<u8> {
        let cfg = dvpn::state::Config {
            authority: upgrade_authority,
            pending_authority: Pubkey::default(),
            mint,
            decimals: 6,
            vault,
            treasury,
            fee_bps: 200,
            min_node_bond: 0,
            min_provider_bond: 0,
            unbonding_seconds: 0,
            channel_close_grace_seconds: 0,
            lease_close_grace_seconds: 0,
            node_listing_ttl_seconds: 0,
            paused_mask,
            bump: 0,
            fees_accrued: 0,
            reserved: [0u8; 64],
        };
        let mut data = Vec::new();
        anchor_lang::AccountSerialize::try_serialize(&cfg, &mut data).unwrap();
        data
    }

    #[test]
    fn require_dvpn_config_matches_refuses_upgrade_authority_mismatch() {
        let upgrade_authority = Pubkey::new_unique();
        let vault = Pubkey::new_unique();
        let mint = Pubkey::new_unique();
        let treasury = Pubkey::new_unique();
        let config = Pubkey::new_unique();
        let mut accounts = HashMap::new();
        accounts.insert(
            config,
            fabricate_config(
                Pubkey::new_unique(),
                vault,
                mint,
                treasury,
                dvpn_core::constants::PAUSE_MASK,
            ),
        );
        let reader = FakeAccountReader(accounts);
        assert!(
            require_dvpn_config_matches(
                &reader,
                &config,
                &upgrade_authority,
                &vault,
                &mint,
                &treasury
            )
            .is_err()
        );
    }

    #[test]
    fn require_dvpn_config_matches_refuses_when_already_unpaused() {
        let upgrade_authority = Pubkey::new_unique();
        let vault = Pubkey::new_unique();
        let mint = Pubkey::new_unique();
        let treasury = Pubkey::new_unique();
        let config = Pubkey::new_unique();
        let mut accounts = HashMap::new();
        accounts.insert(
            config,
            fabricate_config(upgrade_authority, vault, mint, treasury, 0),
        );
        let reader = FakeAccountReader(accounts);
        assert!(
            require_dvpn_config_matches(
                &reader,
                &config,
                &upgrade_authority,
                &vault,
                &mint,
                &treasury
            )
            .is_err()
        );
    }
}
