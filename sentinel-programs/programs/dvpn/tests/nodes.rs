use dvpn::error::DvpnError;
use dvpn_core::args::NodePrices;
use dvpn_core::enums::ListingStatus;
use dvpn_core::seeds::{find_node, find_node_unbonding};
use dvpn_test_support::ix_governance;
use dvpn_test_support::ix_node;
use dvpn_test_support::*;
use solana_sdk::pubkey::Pubkey;
use solana_sdk::signature::Signer;

const TEST_PROTOCOL: u32 = 1;

#[test]
fn open_node_succeeds_delisted() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    let owner = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 5_000);
    let rent_payer = funded_keypair(&mut svm);
    let node_id = [7u8; 32];
    let prices = NodePrices {
        channel_price_per_gb: 10,
        channel_price_per_hour: 20,
        lease_price_per_hour: 30,
    };
    send(
        &mut svm,
        &[ix_node::open_node_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            cfg,
            mint,
            vault,
            owner_token,
            node_id,
            prices,
            TEST_PROTOCOL,
            *b"US",
            b"https://n".to_vec(),
            1_000,
        )],
        &[&owner, &rent_payer],
    )
    .unwrap();
    let node_state = read_node(&svm, &find_node(&dvpn::ID, &owner.pubkey(), &node_id).0);
    assert_eq!(node_state.status, ListingStatus::Delisted as u8);
    assert_eq!(node_state.listed_at, 0);
    assert_eq!(node_state.bond, 1_000);
    assert_eq!(node_state.revenue, 0);
    assert_eq!(node_state.fees_accrued, 0);
    assert_eq!(node_state.channel_price_per_hour, 20);
    assert_solvent(&svm, 0);
}

#[test]
fn open_node_paused_and_bounds() {
    let genesis = genesis_paused();
    let (mut svm, cfg, mint, vault, authority) = (
        genesis.svm,
        genesis.config,
        genesis.mint,
        genesis.vault,
        genesis.authority,
    );
    let owner = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 5_000);
    let rent_payer = funded_keypair(&mut svm);
    let open_node_ix = |node_id, uri, bond| {
        ix_node::open_node_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            cfg,
            mint,
            vault,
            owner_token,
            node_id,
            NodePrices::default(),
            0,
            *b"ZZ",
            uri,
            bond,
        )
    };
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[open_node_ix([1u8; 32], vec![], 1_000)],
            &[&owner, &rent_payer],
        ),
        DvpnError::OperationPaused,
    );
    send(
        &mut svm,
        &[ix_governance::set_paused_mask_ix(&authority.pubkey(), 0)],
        &[&authority],
    )
    .unwrap();
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[open_node_ix([1u8; 32], vec![], 999)],
            &[&owner, &rent_payer],
        ),
        DvpnError::BondBelowMinimum,
    );
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[open_node_ix([1u8; 32], vec![0u8; 129], 1_000)],
            &[&owner, &rent_payer],
        ),
        DvpnError::UriTooLong,
    );
}

#[test]
fn close_node_parks_bond_and_reopen_gated() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    let OpenedNode {
        owner,
        rent_payer,
        node_id,
    } = open_default_node(&mut svm, cfg, mint, vault);
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 0);
    set_clock(&mut svm, 1000);
    send(
        &mut svm,
        &[ix_node::close_node_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            rent_payer.pubkey(),
            node_id,
            owner_token,
            mint,
        )],
        &[&owner, &rent_payer],
    )
    .unwrap();
    let node_unbonding =
        read_node_unbonding(&svm, &find_node_unbonding_address(owner.pubkey(), node_id));
    assert_eq!(node_unbonding.bond, 1000);
    assert_eq!(node_unbonding.closed_at, 1000);
    assert!(is_account_closed(
        &svm,
        &find_node_address(owner.pubkey(), node_id)
    ));

    let second_owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 5_000);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_node::open_node_ix(
                &owner.pubkey(),
                &rent_payer.pubkey(),
                cfg,
                mint,
                vault,
                second_owner_token,
                node_id,
                NodePrices::default(),
                0,
                *b"ZZ",
                vec![],
                1000,
            )],
            &[&owner, &rent_payer],
        ),
        DvpnError::BondPending,
    );

    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_node::withdraw_node_bond_ix(
                &owner.pubkey(),
                node_id,
                owner_token,
                mint,
                rent_payer.pubkey(),
            )],
            &[&owner],
        ),
        DvpnError::UnbondingNotElapsed,
    );
    let unbonding_seconds = read_config(&svm).unbonding_seconds;
    set_clock(&mut svm, 1000 + i64::from(unbonding_seconds));
    svm.expire_blockhash();
    let before = read_token_amount(&svm, &owner_token);
    send(
        &mut svm,
        &[ix_node::withdraw_node_bond_ix(
            &owner.pubkey(),
            node_id,
            owner_token,
            mint,
            rent_payer.pubkey(),
        )],
        &[&owner],
    )
    .unwrap();
    assert_eq!(read_token_amount(&svm, &owner_token), before + 1000);
    assert!(is_account_closed(
        &svm,
        &find_node_unbonding_address(owner.pubkey(), node_id)
    ));
    assert_solvent(&svm, 0);

    send(
        &mut svm,
        &[ix_node::open_node_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            cfg,
            mint,
            vault,
            second_owner_token,
            node_id,
            NodePrices::default(),
            0,
            *b"ZZ",
            vec![],
            1000,
        )],
        &[&owner, &rent_payer],
    )
    .unwrap();
}

#[test]
fn close_node_pays_revenue_and_parks_fees() {
    let genesis = genesis();
    let (mut svm, cfg, mint) = (genesis.svm, genesis.config, genesis.mint);
    let owner = funded_keypair(&mut svm);
    let node = plant_node(
        &mut svm,
        cfg,
        &NodeSpec {
            owner: owner.pubkey(),
            node_id: [5u8; 32],
            revenue: 5,
            fees_accrued: 3,
            bond: 1000,
            rent_recipient: owner.pubkey(),
            ..NodeSpec::default()
        },
    );
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 0);
    let rent_payer = funded_keypair(&mut svm);
    set_clock(&mut svm, 1_000);
    let meta = send_expect_ok(
        &mut svm,
        &[ix_node::close_node_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            owner.pubkey(),
            [5u8; 32],
            owner_token,
            mint,
        )],
        &[&owner, &rent_payer],
    );
    assert!(is_account_closed(&svm, &node));
    assert_eq!(read_token_amount(&svm, &owner_token), 5);
    assert_eq!(read_config(&svm).fees_accrued, 3);
    let unbonding = find_node_unbonding(&dvpn::ID, &owner.pubkey(), &[5u8; 32]).0;
    assert_eq!(read_node_unbonding(&svm, &unbonding).bond, 1000);
    assert_event(
        &meta,
        &dvpn::events::NodeClosed {
            node,
            node_id: [5u8; 32],
            owner: owner.pubkey(),
            bond: 1000,
            unbonding,
            closed_at: 1_000,
            revenue_paid: 5,
            fees_accrued: 3,
        },
    );
    assert_solvent(&svm, 0);
}

#[test]
fn close_node_and_withdraw_node_bond_refunds_rent_recipients() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    let owner = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 5_000);
    let rent_payer = funded_keypair(&mut svm);
    let node_id = [9u8; 32];

    let open = ix_node::open_node_ix_by(
        &owner.pubkey(),
        None,
        &owner.pubkey(),
        &rent_payer.pubkey(),
        cfg,
        mint,
        vault,
        owner_token,
        node_id,
        NodePrices::default(),
        0,
        *b"US",
        vec![],
        1_000,
        owner.pubkey(),
    );
    send_expect_ok(&mut svm, &[open], &[&owner, &rent_payer]);
    let node = find_node_address(owner.pubkey(), node_id);
    let (node_rent, owner_before) = (
        read_lamports(&svm, &node),
        read_lamports(&svm, &owner.pubkey()),
    );

    set_clock(&mut svm, 1_000);
    let close = ix_node::close_node_ix_by(
        &owner.pubkey(),
        None,
        &owner.pubkey(),
        &rent_payer.pubkey(),
        owner.pubkey(),
        node_id,
        owner_token,
        mint,
        owner.pubkey(),
    );
    let fee = send_expect_ok(&mut svm, &[close], &[&owner, &rent_payer]).fee;
    assert_eq!(
        read_lamports(&svm, &owner.pubkey()),
        owner_before + node_rent - fee
    );

    let unbonding = find_node_unbonding_address(owner.pubkey(), node_id);
    let (unbonding_rent, owner_before, rent_payer_before) = (
        read_lamports(&svm, &unbonding),
        read_lamports(&svm, &owner.pubkey()),
        read_lamports(&svm, &rent_payer.pubkey()),
    );
    let unbonding_seconds = read_config(&svm).unbonding_seconds;
    set_clock(&mut svm, 1_000 + i64::from(unbonding_seconds));
    let withdraw =
        ix_node::withdraw_node_bond_ix(&owner.pubkey(), node_id, owner_token, mint, owner.pubkey());
    let fee = send_expect_ok(&mut svm, &[withdraw], &[&owner]).fee;
    assert_eq!(
        read_lamports(&svm, &owner.pubkey()),
        owner_before + unbonding_rent - fee
    );
    assert_eq!(read_lamports(&svm, &rent_payer.pubkey()), rent_payer_before);
}

#[test]
fn withdraw_node_bond_non_owner_is_unauthorized() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    let OpenedNode {
        owner,
        rent_payer,
        node_id,
    } = open_default_node(&mut svm, cfg, mint, vault);
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 0);
    set_clock(&mut svm, 1000);
    send(
        &mut svm,
        &[ix_node::close_node_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            rent_payer.pubkey(),
            node_id,
            owner_token,
            mint,
        )],
        &[&owner, &rent_payer],
    )
    .unwrap();
    let unbonding_seconds = read_config(&svm).unbonding_seconds;
    set_clock(&mut svm, 1000 + i64::from(unbonding_seconds));
    let stranger = funded_keypair(&mut svm);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_node::withdraw_node_bond_ix_by(
                &stranger.pubkey(),
                None,
                &owner.pubkey(),
                node_id,
                owner_token,
                mint,
                rent_payer.pubkey(),
            )],
            &[&stranger],
        ),
        DvpnError::Unauthorized,
    );
}

#[test]
fn withdraw_node_revenue_bounds_and_destination_authority() {
    let genesis = genesis();
    let (mut svm, cfg, mint) = (genesis.svm, genesis.config, genesis.mint);
    let owner = funded_keypair(&mut svm);
    let _node = plant_node(
        &mut svm,
        cfg,
        &NodeSpec {
            owner: owner.pubkey(),
            node_id: [6u8; 32],
            revenue: 10,
            rent_recipient: owner.pubkey(),
            ..NodeSpec::default()
        },
    );
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 0);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_node::withdraw_node_revenue_ix(
                &owner.pubkey(),
                [6u8; 32],
                owner_token,
                0,
                mint,
            )],
            &[&owner],
        ),
        DvpnError::ParamOutOfRange,
    );
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_node::withdraw_node_revenue_ix(
                &owner.pubkey(),
                [6u8; 32],
                owner_token,
                11,
                mint,
            )],
            &[&owner],
        ),
        DvpnError::InsufficientRevenue,
    );

    let not_owner_token = plant_ata(&mut svm, mint, &Pubkey::new_unique(), 0);
    assert!(
        send(
            &mut svm,
            &[ix_node::withdraw_node_revenue_ix(
                &owner.pubkey(),
                [6u8; 32],
                not_owner_token,
                5,
                mint,
            )],
            &[&owner],
        )
        .is_err()
    );
}
