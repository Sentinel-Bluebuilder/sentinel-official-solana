use crate::{ix_grant, ix_node, ix_provider, plant_ata, send};
use dvpn_core::args::{ConfigParamsUpdate, NodePrices};
use dvpn_core::seeds::{
    find_node, find_node_unbonding, find_plan, find_provider, find_provider_unbonding,
    find_subscription,
};
use litesvm::LiteSVM;
use solana_sdk::pubkey::Pubkey;
use solana_sdk::signature::{Keypair, Signer};

pub fn funded_keypair(svm: &mut LiteSVM) -> Keypair {
    let keypair = Keypair::new();
    svm.airdrop(&keypair.pubkey(), 1_000_000_000_000).unwrap();
    keypair
}

pub fn find_grant_address(granter: Pubkey, grantee: Pubkey, target: Pubkey) -> Pubkey {
    dvpn_core::seeds::find_grant(&dvpn::ID, &granter, &grantee, &target).0
}

pub fn open_unscoped_grant(
    svm: &mut LiteSVM,
    granter: &Keypair,
    grantee: Pubkey,
    permissions: u64,
    allowance: u64,
) -> Pubkey {
    let rent_payer = funded_keypair(svm);
    send(
        svm,
        &[ix_grant::open_grant_ix(
            &granter.pubkey(),
            &rent_payer.pubkey(),
            grantee,
            Pubkey::default(),
            permissions,
            0,
            allowance,
            rent_payer.pubkey(),
        )],
        &[granter, &rent_payer],
    )
    .unwrap();
    find_grant_address(granter.pubkey(), grantee, Pubkey::default())
}

pub fn find_node_address(owner: Pubkey, node_id: [u8; 32]) -> Pubkey {
    find_node(&dvpn::ID, &owner, &node_id).0
}

pub fn find_node_unbonding_address(owner: Pubkey, node_id: [u8; 32]) -> Pubkey {
    find_node_unbonding(&dvpn::ID, &owner, &node_id).0
}

pub fn find_provider_address(owner: Pubkey, provider_id: [u8; 32]) -> Pubkey {
    find_provider(&dvpn::ID, &owner, &provider_id).0
}

pub fn find_provider_unbonding_address(owner: Pubkey, provider_id: [u8; 32]) -> Pubkey {
    find_provider_unbonding(&dvpn::ID, &owner, &provider_id).0
}

pub fn find_plan_address(provider: Pubkey, plan_id: [u8; 32]) -> Pubkey {
    find_plan(&dvpn::ID, &provider, &plan_id).0
}

pub fn find_subscription_address(owner: Pubkey, plan: Pubkey) -> Pubkey {
    find_subscription(&dvpn::ID, &owner, &plan).0
}

pub struct OpenedNode {
    pub owner: Keypair,
    pub rent_payer: Keypair,
    pub node_id: [u8; 32],
}

pub fn open_default_node(
    svm: &mut LiteSVM,
    config: Pubkey,
    mint: Pubkey,
    vault: Pubkey,
) -> OpenedNode {
    let owner = funded_keypair(svm);
    let owner_token = plant_ata(svm, mint, &owner.pubkey(), 5_000);
    let rent_payer = funded_keypair(svm);
    let node_id = Pubkey::new_unique().to_bytes();
    send(
        svm,
        &[ix_node::open_node_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            config,
            mint,
            vault,
            owner_token,
            node_id,
            NodePrices::default(),
            0,
            *b"US",
            vec![],
            1_000,
        )],
        &[&owner, &rent_payer],
    )
    .unwrap();
    OpenedNode {
        owner,
        rent_payer,
        node_id,
    }
}

pub struct OpenedProvider {
    pub owner: Keypair,
    pub rent_payer: Keypair,
    pub provider_id: [u8; 32],
}

pub fn open_default_provider(
    svm: &mut LiteSVM,
    config: Pubkey,
    mint: Pubkey,
    vault: Pubkey,
) -> OpenedProvider {
    let owner = funded_keypair(svm);
    let owner_token = plant_ata(svm, mint, &owner.pubkey(), 20_000);
    let rent_payer = funded_keypair(svm);
    let provider_id = Pubkey::new_unique().to_bytes();
    send(
        svm,
        &[ix_provider::open_provider_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            config,
            mint,
            vault,
            owner_token,
            provider_id,
            vec![],
            10_000,
        )],
        &[&owner, &rent_payer],
    )
    .unwrap();
    OpenedProvider {
        owner,
        rent_payer,
        provider_id,
    }
}

pub fn params_with_fee_bps(value: u16) -> ConfigParamsUpdate {
    ConfigParamsUpdate {
        fee_bps: Some(value),
        ..Default::default()
    }
}

pub fn params_with_unbonding_seconds(value: u32) -> ConfigParamsUpdate {
    ConfigParamsUpdate {
        unbonding_seconds: Some(value),
        ..Default::default()
    }
}

pub fn params_with_channel_grace_seconds(value: u32) -> ConfigParamsUpdate {
    ConfigParamsUpdate {
        channel_close_grace_seconds: Some(value),
        ..Default::default()
    }
}

pub fn params_with_lease_grace_seconds(value: u32) -> ConfigParamsUpdate {
    ConfigParamsUpdate {
        lease_close_grace_seconds: Some(value),
        ..Default::default()
    }
}

pub fn params_with_node_listing_ttl_seconds(value: u32) -> ConfigParamsUpdate {
    ConfigParamsUpdate {
        node_listing_ttl_seconds: Some(value),
        ..Default::default()
    }
}
