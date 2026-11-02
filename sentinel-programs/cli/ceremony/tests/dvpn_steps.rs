use solana_pubkey::Pubkey;

#[test]
fn create_fee_treasury_is_idempotent_owner_ata_distinct_from_vault() {
    let payer = Pubkey::new_unique();
    let fee_treasury_owner = Pubkey::new_unique();
    let mint = Pubkey::new_unique();
    let config = Pubkey::new_unique();
    let step = ceremony::dvpn_steps::build_dvpn_create_fee_treasury_step(
        &payer,
        &fee_treasury_owner,
        &mint,
        &config,
    )
    .unwrap();
    assert_eq!(
        step.name,
        ceremony::dvpn_steps::STEP_DVPN_CREATE_FEE_TREASURY
    );
    let treasury = ceremony::dvpn_steps::fee_treasury_address(&fee_treasury_owner, &mint);
    let vault = ceremony::dvpn_steps::dvpn_vault_address(&config, &mint);
    assert_ne!(treasury, vault);
    assert!(step.ixs.iter().any(|instruction| {
        instruction
            .accounts
            .iter()
            .any(|account_meta| account_meta.pubkey == treasury)
    }));
}

#[test]
fn default_initial_config_params_ship_fully_paused() {
    let params = ceremony::dvpn_steps::default_initial_config_params();
    assert_eq!(params.paused_mask, dvpn_core::constants::PAUSE_MASK);
    assert_eq!(params.fee_bps, 200);
    assert_eq!(params.unbonding_seconds, 604_800);
    assert_eq!(params.channel_close_grace_seconds, 86_400);
    assert_eq!(params.lease_close_grace_seconds, 86_400);
    assert_eq!(params.node_listing_ttl_seconds, 86_400);
    assert_eq!(params.min_node_bond, 1_000_000_000);
    assert_eq!(params.min_provider_bond, 10_000_000_000);
    assert!(params.fee_bps <= dvpn_core::constants::MAX_FEE_BPS);
}
