use dvpn::error::DvpnError;
use dvpn_core::enums::ListingStatus;
use dvpn_core::permissions::*;
use dvpn_test_support::*;
use dvpn_test_support::{ix_grant, ix_node};
use solana_sdk::pubkey::Pubkey;
use solana_sdk::signature::{Keypair, Signer};

#[test]
fn open_grant_stores_fields_and_rejects_empty_permissions() {
    let genesis = genesis_paused();
    let mut svm = genesis.svm;
    set_clock(&mut svm, 1_000);
    let granter = funded_keypair(&mut svm);
    let rent_payer = funded_keypair(&mut svm);
    let grantee = Keypair::new();
    let zero = Pubkey::default();

    let empty = ix_grant::open_grant_ix(
        &granter.pubkey(),
        &rent_payer.pubkey(),
        grantee.pubkey(),
        zero,
        0,
        0,
        0,
        rent_payer.pubkey(),
    );
    assert_dvpn_error(
        &send_expect_err(&mut svm, &[empty], &[&granter, &rent_payer]),
        DvpnError::ParamOutOfRange,
    );

    let target = Pubkey::new_unique();
    let instruction = ix_grant::open_grant_ix(
        &granter.pubkey(),
        &rent_payer.pubkey(),
        grantee.pubkey(),
        target,
        PERM_ALL,
        2_000,
        7,
        rent_payer.pubkey(),
    );
    send(&mut svm, &[instruction], &[&granter, &rent_payer]).unwrap();
    let stored = read_grant(
        &svm,
        &find_grant_address(granter.pubkey(), grantee.pubkey(), target),
    );
    assert_eq!(stored.granter, granter.pubkey());
    assert_eq!(stored.grantee, grantee.pubkey());
    assert_eq!(stored.target, target);
    assert_eq!(stored.permissions, PERM_ALL);
    assert_eq!(stored.expires_at, 2_000);
    assert_eq!(stored.allowance, 7);
    assert_eq!(stored.rent_recipient, rent_payer.pubkey());
    assert_eq!(stored.created_at, 1_000);
}

#[test]
fn open_grant_rejects_missing_rent_recipient() {
    let genesis = genesis_paused();
    let mut svm = genesis.svm;
    set_clock(&mut svm, 1_000);
    let granter = funded_keypair(&mut svm);
    let rent_payer = funded_keypair(&mut svm);

    let instruction = ix_grant::open_grant_ix(
        &granter.pubkey(),
        &rent_payer.pubkey(),
        Pubkey::new_unique(),
        Pubkey::default(),
        PERM_ALL,
        0,
        0,
        Pubkey::default(),
    );
    assert_dvpn_error(
        &send_expect_err(&mut svm, &[instruction], &[&granter, &rent_payer]),
        DvpnError::MissingRentRecipient,
    );
}

#[test]
fn open_grant_rejects_stranger_rent_recipient() {
    let genesis = genesis_paused();
    let mut svm = genesis.svm;
    set_clock(&mut svm, 1_000);
    let granter = funded_keypair(&mut svm);
    let rent_payer = funded_keypair(&mut svm);

    let instruction = ix_grant::open_grant_ix(
        &granter.pubkey(),
        &rent_payer.pubkey(),
        Pubkey::new_unique(),
        Pubkey::default(),
        PERM_ALL,
        0,
        0,
        Pubkey::new_unique(),
    );
    assert_dvpn_error(
        &send_expect_err(&mut svm, &[instruction], &[&granter, &rent_payer]),
        DvpnError::InvalidRentRecipient,
    );
}

#[test]
fn update_grant_is_granter_only() {
    let genesis = genesis_paused();
    let mut svm = genesis.svm;
    set_clock(&mut svm, 1_000);
    let granter = funded_keypair(&mut svm);
    let grantee = funded_keypair(&mut svm);
    let pda = open_unscoped_grant(&mut svm, &granter, grantee.pubkey(), PERM_LIST_NODE, 5);

    let denied = ix_grant::update_grant_ix(&grantee.pubkey(), pda, None, None, Some(1));
    assert_dvpn_error(
        &send_expect_err(&mut svm, &[denied], &[&grantee]),
        DvpnError::Unauthorized,
    );
    let instruction = ix_grant::update_grant_ix(
        &granter.pubkey(),
        pda,
        Some(PERM_UPDATE_NODE),
        Some(3_000),
        Some(9),
    );
    send(&mut svm, &[instruction], &[&granter]).unwrap();
    let stored = read_grant(&svm, &pda);
    assert_eq!(
        (stored.permissions, stored.expires_at, stored.allowance),
        (PERM_UPDATE_NODE, 3_000, 9)
    );
}

#[test]
fn close_grant_refunds_rent_recipient_and_stranger_needs_expiry() {
    let genesis = genesis_paused();
    let mut svm = genesis.svm;
    set_clock(&mut svm, 1_000);
    let granter = funded_keypair(&mut svm);
    let grantee = funded_keypair(&mut svm);
    let stranger = funded_keypair(&mut svm);
    let rent_payer = funded_keypair(&mut svm);
    let open = |svm: &mut litesvm::LiteSVM, target: Pubkey, expires_at: i64| {
        let instruction = ix_grant::open_grant_ix(
            &granter.pubkey(),
            &rent_payer.pubkey(),
            grantee.pubkey(),
            target,
            PERM_LIST_NODE,
            expires_at,
            0,
            rent_payer.pubkey(),
        );
        send(svm, &[instruction], &[&granter, &rent_payer]).unwrap();
        find_grant_address(granter.pubkey(), grantee.pubkey(), target)
    };
    let rent_payer_lamports =
        |svm: &litesvm::LiteSVM| svm.get_account(&rent_payer.pubkey()).unwrap().lamports;

    let unscoped_grant = open(&mut svm, Pubkey::default(), 0);
    let (before, rent) = (
        rent_payer_lamports(&svm),
        svm.get_account(&unscoped_grant).unwrap().lamports,
    );
    let instruction =
        ix_grant::close_grant_ix(&granter.pubkey(), unscoped_grant, rent_payer.pubkey());
    send(&mut svm, &[instruction], &[&granter]).unwrap();
    assert!(is_account_closed(&svm, &unscoped_grant));
    assert_eq!(rent_payer_lamports(&svm), before + rent);

    let scoped_grant = open(&mut svm, Pubkey::new_unique(), 2_000);
    set_clock(&mut svm, 1_999);
    let early = ix_grant::close_grant_ix(&stranger.pubkey(), scoped_grant, rent_payer.pubkey());
    assert_dvpn_error(
        &send_expect_err(&mut svm, &[early], &[&stranger]),
        DvpnError::Unauthorized,
    );
    set_clock(&mut svm, 2_000);
    svm.expire_blockhash();
    let instruction =
        ix_grant::close_grant_ix(&stranger.pubkey(), scoped_grant, rent_payer.pubkey());
    send(&mut svm, &[instruction], &[&stranger]).unwrap();
    assert!(is_account_closed(&svm, &scoped_grant));
}

#[test]
fn close_grant_refunds_rent_recipient() {
    let genesis = genesis_paused();
    let mut svm = genesis.svm;
    set_clock(&mut svm, 1_000);
    let granter = funded_keypair(&mut svm);
    let grantee = Keypair::new();
    let rent_payer = funded_keypair(&mut svm);
    let grant = find_grant_address(granter.pubkey(), grantee.pubkey(), Pubkey::default());

    let instruction = ix_grant::open_grant_ix(
        &granter.pubkey(),
        &rent_payer.pubkey(),
        grantee.pubkey(),
        Pubkey::default(),
        PERM_ALL,
        0,
        0,
        granter.pubkey(),
    );
    let meta = send(&mut svm, &[instruction], &[&granter, &rent_payer]).unwrap();
    assert_event(
        &meta,
        &dvpn::events::GrantOpened {
            grant,
            granter: granter.pubkey(),
            grantee: grantee.pubkey(),
            target: Pubkey::default(),
            permissions: PERM_ALL,
            expires_at: 0,
            allowance: 0,
            grant_rent_recipient: granter.pubkey(),
        },
    );

    let (rent_payer_before, granter_before, rent) = (
        read_lamports(&svm, &rent_payer.pubkey()),
        read_lamports(&svm, &granter.pubkey()),
        read_lamports(&svm, &grant),
    );
    let instruction = ix_grant::close_grant_ix(&granter.pubkey(), grant, granter.pubkey());
    let fee = send(&mut svm, &[instruction], &[&granter]).unwrap().fee;
    assert!(is_account_closed(&svm, &grant));
    assert_eq!(
        read_lamports(&svm, &granter.pubkey()),
        granter_before + rent - fee
    );
    assert_eq!(read_lamports(&svm, &rent_payer.pubkey()), rent_payer_before);
}

#[test]
fn grant_gates_node_listing() {
    let genesis = genesis();
    let (mut svm, cfg) = (genesis.svm, genesis.config);
    set_clock(&mut svm, 1_999);
    let owner = funded_keypair(&mut svm);
    let grantee = funded_keypair(&mut svm);
    let rent_payer = funded_keypair(&mut svm);
    let spec = NodeSpec {
        owner: owner.pubkey(),
        node_id: [3u8; 32],
        ..Default::default()
    };
    let node = plant_node(&mut svm, cfg, &spec);
    let list = |svm: &mut litesvm::LiteSVM, grant: Pubkey| {
        svm.expire_blockhash();
        let instruction =
            ix_node::list_node_ix_by(&grantee.pubkey(), Some(grant), &owner.pubkey(), [3u8; 32]);
        send_expect_err(svm, &[instruction], &[&grantee])
    };
    let open = |svm: &mut litesvm::LiteSVM, target: Pubkey, perms: u64, expires_at: i64| {
        let instruction = ix_grant::open_grant_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            grantee.pubkey(),
            target,
            perms,
            expires_at,
            0,
            rent_payer.pubkey(),
        );
        send(svm, &[instruction], &[&owner, &rent_payer]).unwrap();
        find_grant_address(owner.pubkey(), grantee.pubkey(), target)
    };

    let unscoped = open(&mut svm, Pubkey::default(), PERM_UPDATE_NODE, 0);
    assert_dvpn_error(&list(&mut svm, unscoped), DvpnError::PermissionNotGranted);

    let useless = open(&mut svm, owner.pubkey(), PERM_LIST_NODE, 0);
    assert_dvpn_error(&list(&mut svm, useless), DvpnError::GrantTargetMismatch);

    let scoped = open(&mut svm, node, PERM_LIST_NODE, 2_000);
    svm.expire_blockhash();
    let instruction =
        ix_node::list_node_ix_by(&grantee.pubkey(), Some(scoped), &owner.pubkey(), [3u8; 32]);
    let meta = send(&mut svm, &[instruction], &[&grantee]).unwrap();
    assert_event(
        &meta,
        &dvpn::events::GrantUsed {
            grant: scoped,
            granter: owner.pubkey(),
            grantee: grantee.pubkey(),
            permission: PERM_LIST_NODE,
            target: node,
        },
    );
    assert_eq!(read_node(&svm, &node).status, ListingStatus::Listed as u8);

    set_clock(&mut svm, 2_000);
    assert_dvpn_error(&list(&mut svm, scoped), DvpnError::GrantExpired);
}
