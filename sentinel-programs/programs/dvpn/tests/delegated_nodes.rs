use dvpn::error::DvpnError;
use dvpn_core::args::NodePrices;
use dvpn_core::permissions::*;
use dvpn_core::seeds::find_node;
use dvpn_test_support::*;
use dvpn_test_support::{ix_grant, ix_node};
use solana_sdk::pubkey::Pubkey;
use solana_sdk::signature::Signer;

#[test]
fn grantee_opens_node_with_bond_through_delegate() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    let owner = funded_keypair(&mut svm);
    let grantee = funded_keypair(&mut svm);
    let rent_payer = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 5_000);
    approve_delegate(&mut svm, &owner, owner_token, mint, cfg, 5_000);
    let grant = open_unscoped_grant(&mut svm, &owner, grantee.pubkey(), PERM_OPEN_NODE, 1_500);
    let open = |node_id: [u8; 32], grant: Option<Pubkey>| {
        ix_node::open_node_ix_by(
            &grantee.pubkey(),
            grant,
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
            rent_payer.pubkey(),
        )
    };

    send(
        &mut svm,
        &[open([7u8; 32], Some(grant))],
        &[&grantee, &rent_payer],
    )
    .unwrap();
    let node = read_node(&svm, &find_node(&dvpn::ID, &owner.pubkey(), &[7u8; 32]).0);
    assert_eq!(node.owner, owner.pubkey());
    assert_eq!(read_grant(&svm, &grant).allowance, 500);
    assert_eq!(read_token_amount(&svm, &vault), 1_000);

    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[open([8u8; 32], Some(grant))],
            &[&grantee, &rent_payer],
        ),
        DvpnError::AmountExceedsAllowance,
    );
    assert_dvpn_error(
        &send_expect_err(&mut svm, &[open([9u8; 32], None)], &[&grantee, &rent_payer]),
        DvpnError::Unauthorized,
    );
}

#[test]
fn grantee_closes_node_through_scoped_grant() {
    let genesis = genesis();
    let (mut svm, cfg, mint) = (genesis.svm, genesis.config, genesis.mint);
    let owner = funded_keypair(&mut svm);
    let grantee = funded_keypair(&mut svm);
    let rent_payer = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 0);
    let spec = NodeSpec {
        owner: owner.pubkey(),
        node_id: [7u8; 32],
        bond: 1_000,
        rent_recipient: rent_payer.pubkey(),
        ..Default::default()
    };
    let node = plant_node(&mut svm, cfg, &spec);
    let instruction = ix_grant::open_grant_ix(
        &owner.pubkey(),
        &rent_payer.pubkey(),
        grantee.pubkey(),
        node,
        PERM_CLOSE_NODE,
        0,
        0,
        rent_payer.pubkey(),
    );
    send(&mut svm, &[instruction], &[&owner, &rent_payer]).unwrap();
    let scoped = find_grant_address(owner.pubkey(), grantee.pubkey(), node);

    let instruction = ix_node::close_node_ix_by(
        &grantee.pubkey(),
        Some(scoped),
        &owner.pubkey(),
        &rent_payer.pubkey(),
        rent_payer.pubkey(),
        [7u8; 32],
        owner_token,
        mint,
        rent_payer.pubkey(),
    );
    send(&mut svm, &[instruction], &[&grantee, &rent_payer]).unwrap();
    assert!(is_account_closed(&svm, &node));
}

#[test]
fn grantee_withdraws_node_bond_only_to_owner_token() {
    let genesis = genesis();
    let (mut svm, cfg, mint) = (genesis.svm, genesis.config, genesis.mint);
    set_clock(&mut svm, 1_000);
    let owner = funded_keypair(&mut svm);
    let grantee = funded_keypair(&mut svm);
    let rent_payer = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 0);
    let grantee_token = plant_ata(&mut svm, mint, &grantee.pubkey(), 0);
    let spec = NodeSpec {
        owner: owner.pubkey(),
        node_id: [7u8; 32],
        bond: 1_000,
        rent_recipient: rent_payer.pubkey(),
        ..Default::default()
    };
    let node = plant_node(&mut svm, cfg, &spec);
    let instruction = ix_node::close_node_ix(
        &owner.pubkey(),
        &rent_payer.pubkey(),
        rent_payer.pubkey(),
        [7u8; 32],
        owner_token,
        mint,
    );
    send(&mut svm, &[instruction], &[&owner, &rent_payer]).unwrap();
    let instruction = ix_grant::open_grant_ix(
        &owner.pubkey(),
        &rent_payer.pubkey(),
        grantee.pubkey(),
        node,
        PERM_WITHDRAW_NODE_BOND,
        0,
        0,
        rent_payer.pubkey(),
    );
    send(&mut svm, &[instruction], &[&owner, &rent_payer]).unwrap();
    let scoped = find_grant_address(owner.pubkey(), grantee.pubkey(), node);
    let unbonding_seconds = i64::from(read_config(&svm).unbonding_seconds);
    set_clock(&mut svm, 1_000 + unbonding_seconds);
    let withdraw = |token: Pubkey| {
        ix_node::withdraw_node_bond_ix_by(
            &grantee.pubkey(),
            Some(scoped),
            &owner.pubkey(),
            [7u8; 32],
            token,
            mint,
            rent_payer.pubkey(),
        )
    };

    assert_anchor_error(
        &send_expect_err(&mut svm, &[withdraw(grantee_token)], &[&grantee]),
        anchor_lang::error::ErrorCode::ConstraintTokenOwner,
    );
    svm.expire_blockhash();
    send(&mut svm, &[withdraw(owner_token)], &[&grantee]).unwrap();
    assert_eq!(read_token_amount(&svm, &owner_token), 1_000);
}

#[test]
fn grantee_withdraws_node_revenue_only_to_owner_token() {
    let genesis = genesis();
    let (mut svm, cfg, mint) = (genesis.svm, genesis.config, genesis.mint);
    let owner = funded_keypair(&mut svm);
    let grantee = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 0);
    let grantee_token = plant_ata(&mut svm, mint, &grantee.pubkey(), 0);
    let node = plant_node(
        &mut svm,
        cfg,
        &NodeSpec {
            owner: owner.pubkey(),
            node_id: [5u8; 32],
            revenue: 300,
            ..Default::default()
        },
    );
    let grant = open_unscoped_grant(
        &mut svm,
        &owner,
        grantee.pubkey(),
        PERM_WITHDRAW_NODE_REVENUE,
        0,
    );
    let withdraw = |token: Pubkey| {
        ix_node::withdraw_node_revenue_ix_by(
            &grantee.pubkey(),
            Some(grant),
            &owner.pubkey(),
            [5u8; 32],
            token,
            100,
            mint,
        )
    };

    assert_anchor_error(
        &send_expect_err(&mut svm, &[withdraw(grantee_token)], &[&grantee]),
        anchor_lang::error::ErrorCode::ConstraintTokenOwner,
    );
    svm.expire_blockhash();
    send(&mut svm, &[withdraw(owner_token)], &[&grantee]).unwrap();
    assert_eq!(read_token_amount(&svm, &owner_token), 100);
    assert_eq!(read_node(&svm, &node).revenue, 200);
}
