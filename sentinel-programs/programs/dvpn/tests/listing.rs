use dvpn::error::DvpnError;
use dvpn_core::enums::ListingStatus;
use dvpn_core::permissions::*;
use dvpn_core::seeds::find_subscription;
use dvpn_test_support::*;
use dvpn_test_support::{ix_grant, ix_node, ix_plan, ix_provider, ix_subscription};
use solana_sdk::pubkey::Pubkey;
use solana_sdk::signature::{Keypair, Signer};

#[test]
fn list_node_is_heartbeat() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    let OpenedNode { owner, node_id, .. } = open_default_node(&mut svm, cfg, mint, vault);
    set_clock(&mut svm, 1000);
    send(
        &mut svm,
        &[ix_node::list_node_ix(&owner.pubkey(), node_id)],
        &[&owner],
    )
    .unwrap();
    assert_eq!(
        read_node(&svm, &find_node_address(owner.pubkey(), node_id)).listed_at,
        1000
    );
    set_clock(&mut svm, 2000);
    svm.expire_blockhash();
    send(
        &mut svm,
        &[ix_node::list_node_ix(&owner.pubkey(), node_id)],
        &[&owner],
    )
    .unwrap();
    let node_state = read_node(&svm, &find_node_address(owner.pubkey(), node_id));
    assert_eq!(node_state.listed_at, 2000);
    assert_eq!(node_state.status, ListingStatus::Listed as u8);
    send(
        &mut svm,
        &[ix_node::delist_node_ix(&owner.pubkey(), node_id)],
        &[&owner],
    )
    .unwrap();
    let node_state = read_node(&svm, &find_node_address(owner.pubkey(), node_id));
    assert_eq!(node_state.status, ListingStatus::Delisted as u8);
    assert_eq!(node_state.listed_at, 0);
    assert_solvent(&svm, 0);
}

#[test]
fn node_admin_grantee_and_stranger() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    let OpenedNode { owner, node_id, .. } = open_default_node(&mut svm, cfg, mint, vault);
    let grantee = Keypair::new();
    let rent_payer = funded_keypair(&mut svm);
    send(
        &mut svm,
        &[ix_grant::open_grant_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            grantee.pubkey(),
            Pubkey::default(),
            PERM_LIST_NODE | PERM_DELIST_NODE | PERM_UPDATE_NODE,
            0,
            0,
            rent_payer.pubkey(),
        )],
        &[&owner, &rent_payer],
    )
    .unwrap();
    svm.airdrop(&grantee.pubkey(), 10_000_000_000).unwrap();
    send(
        &mut svm,
        &[ix_node::list_node_ix_as_grantee(
            &grantee.pubkey(),
            &owner.pubkey(),
            &find_grant_address(owner.pubkey(), grantee.pubkey(), Pubkey::default()),
            node_id,
        )],
        &[&grantee],
    )
    .unwrap();
    assert_eq!(
        read_node(&svm, &find_node_address(owner.pubkey(), node_id)).status,
        1
    );

    let stranger = funded_keypair(&mut svm);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_node::list_node_ix_by(
                &stranger.pubkey(),
                None,
                &owner.pubkey(),
                node_id,
            )],
            &[&stranger],
        ),
        DvpnError::Unauthorized,
    );

    send(
        &mut svm,
        &[ix_node::update_node_ix(
            &owner.pubkey(),
            node_id,
            None,
            None,
            None,
            None,
        )],
        &[&owner],
    )
    .unwrap();
    assert_solvent(&svm, 0);
}

#[test]
fn provider_admin_grantee_and_stranger() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    let OpenedProvider {
        owner, provider_id, ..
    } = open_default_provider(&mut svm, cfg, mint, vault);
    let grantee = Keypair::new();
    let rent_payer = funded_keypair(&mut svm);
    send(
        &mut svm,
        &[ix_grant::open_grant_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            grantee.pubkey(),
            Pubkey::default(),
            PERM_LIST_PROVIDER | PERM_DELIST_PROVIDER | PERM_UPDATE_PROVIDER,
            0,
            0,
            rent_payer.pubkey(),
        )],
        &[&owner, &rent_payer],
    )
    .unwrap();
    svm.airdrop(&grantee.pubkey(), 10_000_000_000).unwrap();
    send(
        &mut svm,
        &[ix_provider::list_provider_ix_as_grantee(
            &grantee.pubkey(),
            &owner.pubkey(),
            &find_grant_address(owner.pubkey(), grantee.pubkey(), Pubkey::default()),
            provider_id,
        )],
        &[&grantee],
    )
    .unwrap();
    assert_eq!(
        read_provider(&svm, &find_provider_address(owner.pubkey(), provider_id)).status,
        1
    );
    send(
        &mut svm,
        &[ix_provider::update_provider_ix_as_grantee(
            &grantee.pubkey(),
            &owner.pubkey(),
            &find_grant_address(owner.pubkey(), grantee.pubkey(), Pubkey::default()),
            provider_id,
            Some(b"https://op".to_vec()),
        )],
        &[&grantee],
    )
    .unwrap();
    let updated = read_provider(&svm, &find_provider_address(owner.pubkey(), provider_id));
    assert_eq!(updated.uri_len, 10);
    send(
        &mut svm,
        &[ix_provider::delist_provider_ix_as_grantee(
            &grantee.pubkey(),
            &owner.pubkey(),
            &find_grant_address(owner.pubkey(), grantee.pubkey(), Pubkey::default()),
            provider_id,
        )],
        &[&grantee],
    )
    .unwrap();
    assert_eq!(
        read_provider(&svm, &find_provider_address(owner.pubkey(), provider_id)).status,
        0
    );

    let stranger = funded_keypair(&mut svm);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_provider::list_provider_ix_by(
                &stranger.pubkey(),
                None,
                &owner.pubkey(),
                provider_id,
            )],
            &[&stranger],
        ),
        DvpnError::Unauthorized,
    );
    assert_solvent(&svm, 0);
}

#[test]
fn open_subscription_gated_by_plan_then_provider_listing() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    let OpenedProvider {
        owner, provider_id, ..
    } = open_default_provider(&mut svm, cfg, mint, vault);
    let provider = find_provider_address(owner.pubkey(), provider_id);
    let plan_id = [42u8; 32];
    send(
        &mut svm,
        &[ix_plan::open_plan_ix(
            &owner.pubkey(),
            provider,
            plan_id,
            0,
            3600,
            0,
            4,
        )],
        &[&owner],
    )
    .unwrap();
    let plan = find_plan_address(provider, plan_id);
    let rent_payer = funded_keypair(&mut svm);
    let wallet = Pubkey::new_unique();

    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_subscription::open_subscription_ix_free(
                &owner.pubkey(),
                &rent_payer.pubkey(),
                wallet,
                provider,
                plan,
                3600,
                0,
                4,
                None,
                rent_payer.pubkey(),
            )],
            &[&owner, &rent_payer],
        ),
        DvpnError::PlanNotListed,
    );

    send(
        &mut svm,
        &[ix_plan::list_plan_ix(&owner.pubkey(), provider, plan_id)],
        &[&owner],
    )
    .unwrap();
    svm.expire_blockhash();
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_subscription::open_subscription_ix_free(
                &owner.pubkey(),
                &rent_payer.pubkey(),
                wallet,
                provider,
                plan,
                3600,
                0,
                4,
                None,
                rent_payer.pubkey(),
            )],
            &[&owner, &rent_payer],
        ),
        DvpnError::ProviderNotListed,
    );

    send(
        &mut svm,
        &[ix_provider::list_provider_ix(&owner.pubkey(), provider_id)],
        &[&owner],
    )
    .unwrap();
    svm.expire_blockhash();
    send(
        &mut svm,
        &[ix_subscription::open_subscription_ix_free(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            wallet,
            provider,
            plan,
            3600,
            0,
            4,
            None,
            rent_payer.pubkey(),
        )],
        &[&owner, &rent_payer],
    )
    .unwrap();
    assert_eq!(
        read_subscription(&svm, &find_subscription(&dvpn::ID, &wallet, &plan).0).price,
        0
    );
    assert_solvent(&svm, 0);
}
