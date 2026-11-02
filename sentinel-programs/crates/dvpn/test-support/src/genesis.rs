use crate::{
    PROGRAM_ID, TEST_MINT_DECIMALS, build_litesvm, deploy_dvpn, plant_account, plant_ata,
    plant_sealed_mint, plant_vault,
};
use dvpn::state::Config;
use dvpn_core::args::InitialConfigParams;
use dvpn_core::constants::{
    MIN_GRACE_SECONDS, MIN_NODE_LISTING_TTL_SECONDS, MIN_UNBONDING_SECONDS, PAUSE_MASK,
};
use litesvm::LiteSVM;
use solana_sdk::{pubkey::Pubkey, signature::Keypair, signer::Signer};

pub fn default_initial_config_params() -> InitialConfigParams {
    InitialConfigParams {
        fee_bps: 0,
        min_node_bond: 1_000,
        min_provider_bond: 1_000,
        unbonding_seconds: MIN_UNBONDING_SECONDS,
        channel_close_grace_seconds: MIN_GRACE_SECONDS,
        lease_close_grace_seconds: MIN_GRACE_SECONDS,
        node_listing_ttl_seconds: MIN_NODE_LISTING_TTL_SECONDS,
        paused_mask: 0,
    }
}

pub struct Genesis {
    pub svm: LiteSVM,
    pub config: Pubkey,
    pub mint: Pubkey,
    pub vault: Pubkey,
    pub treasury: Pubkey,
    pub authority: Keypair,
}

fn build_genesis(paused_mask: u8) -> Genesis {
    let mut svm = build_litesvm();
    let authority = Keypair::new();
    svm.airdrop(&authority.pubkey(), 1_000_000_000_000)
        .expect("airdrop to genesis authority");
    deploy_dvpn(&mut svm, &authority);

    let mint = plant_sealed_mint(&mut svm);
    let (config, bump) = dvpn_core::seeds::find_config(&PROGRAM_ID);
    let vault = plant_vault(&mut svm, config, mint);
    let treasury = plant_ata(&mut svm, mint, &Pubkey::new_unique(), 0);

    let params = InitialConfigParams {
        paused_mask,
        ..default_initial_config_params()
    };
    plant_account(
        &mut svm,
        config,
        &Config {
            authority: authority.pubkey(),
            pending_authority: Pubkey::default(),
            mint,
            decimals: TEST_MINT_DECIMALS,
            vault,
            treasury,
            fee_bps: params.fee_bps,
            min_node_bond: params.min_node_bond,
            min_provider_bond: params.min_provider_bond,
            unbonding_seconds: params.unbonding_seconds,
            channel_close_grace_seconds: params.channel_close_grace_seconds,
            lease_close_grace_seconds: params.lease_close_grace_seconds,
            node_listing_ttl_seconds: params.node_listing_ttl_seconds,
            paused_mask: params.paused_mask,
            bump,
            fees_accrued: 0,
            reserved: [0u8; 64],
        },
    );

    Genesis {
        svm,
        config,
        mint,
        vault,
        treasury,
        authority,
    }
}

pub fn genesis_paused() -> Genesis {
    build_genesis(PAUSE_MASK)
}

pub fn genesis() -> Genesis {
    build_genesis(0)
}
