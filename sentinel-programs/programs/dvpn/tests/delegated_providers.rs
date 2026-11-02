use dvpn::error::DvpnError;
use dvpn_core::enums::ListingStatus;
use dvpn_core::permissions::*;
use dvpn_core::seeds::{find_provider, find_provider_unbonding};
use dvpn_test_support::ix_provider;
use dvpn_test_support::*;
use litesvm::LiteSVM;
use solana_sdk::pubkey::Pubkey;
use solana_sdk::signature::{Keypair, Signer};

fn open_provider(
    svm: &mut LiteSVM,
    cfg: Pubkey,
    mint: Pubkey,
    vault: Pubkey,
    owner: &Keypair,
    provider_id: [u8; 32],
) -> Pubkey {
    let rent_payer = funded_keypair(svm);
    let owner_token = plant_ata(svm, mint, &owner.pubkey(), 5_000);
    send(
        svm,
        &[ix_provider::open_provider_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            cfg,
            mint,
            vault,
            owner_token,
            provider_id,
            vec![],
            1_000,
        )],
        &[owner, &rent_payer],
    )
    .unwrap();
    find_provider(&dvpn::ID, &owner.pubkey(), &provider_id).0
}

#[test]
fn grantee_lists_provider() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    let owner = funded_keypair(&mut svm);
    let grantee = funded_keypair(&mut svm);
    let provider = open_provider(&mut svm, cfg, mint, vault, &owner, [3u8; 32]);
    open_unscoped_grant(&mut svm, &owner, grantee.pubkey(), PERM_LIST_PROVIDER, 0);
    send(
        &mut svm,
        &[ix_provider::list_provider_ix_as_grantee(
            &grantee.pubkey(),
            &owner.pubkey(),
            &find_grant_address(owner.pubkey(), grantee.pubkey(), Pubkey::default()),
            [3u8; 32],
        )],
        &[&grantee],
    )
    .unwrap();
    assert_eq!(
        read_provider(&svm, &provider).status,
        ListingStatus::Listed as u8
    );
}

#[test]
fn grantee_opens_provider_with_bond_through_delegate() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    let owner = funded_keypair(&mut svm);
    let grantee = funded_keypair(&mut svm);
    let rent_payer = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 5_000);
    approve_delegate(&mut svm, &owner, owner_token, mint, cfg, 5_000);
    let grant = open_unscoped_grant(
        &mut svm,
        &owner,
        grantee.pubkey(),
        PERM_OPEN_PROVIDER,
        1_500,
    );
    let open = |provider_id: [u8; 32], grant: Option<Pubkey>| {
        ix_provider::open_provider_ix_by(
            &grantee.pubkey(),
            grant,
            &owner.pubkey(),
            &rent_payer.pubkey(),
            cfg,
            mint,
            vault,
            owner_token,
            provider_id,
            vec![],
            1_000,
            grantee.pubkey(),
        )
    };
    send(
        &mut svm,
        &[open([7u8; 32], Some(grant))],
        &[&grantee, &rent_payer],
    )
    .unwrap();
    let provider = read_provider(
        &svm,
        &find_provider(&dvpn::ID, &owner.pubkey(), &[7u8; 32]).0,
    );
    assert_eq!(provider.owner, owner.pubkey());
    assert_eq!(provider.bond, 1_000);
    assert_eq!(provider.rent_recipient, grantee.pubkey());
    assert_eq!(read_grant(&svm, &grant).allowance, 500);
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
    assert_solvent(&svm, 0);
}

#[test]
fn grantee_closes_provider_through_grant() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    set_clock(&mut svm, 1_000);
    let owner = funded_keypair(&mut svm);
    let grantee = funded_keypair(&mut svm);
    let rent_payer = funded_keypair(&mut svm);
    let provider = open_provider(&mut svm, cfg, mint, vault, &owner, [7u8; 32]);
    let rent_recipient = read_provider(&svm, &provider).rent_recipient;
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 0);
    let grant = open_unscoped_grant(&mut svm, &owner, grantee.pubkey(), PERM_CLOSE_PROVIDER, 0);
    send(
        &mut svm,
        &[ix_provider::close_provider_ix_by(
            &grantee.pubkey(),
            Some(grant),
            &owner.pubkey(),
            &rent_payer.pubkey(),
            rent_recipient,
            [7u8; 32],
            owner_token,
            mint,
            grantee.pubkey(),
        )],
        &[&grantee, &rent_payer],
    )
    .unwrap();
    assert!(is_account_closed(&svm, &provider));
    let unbonding = find_provider_unbonding(&dvpn::ID, &owner.pubkey(), &[7u8; 32]).0;
    let unbonding = read_provider_unbonding(&svm, &unbonding);
    assert_eq!(
        (unbonding.bond, unbonding.rent_recipient),
        (1_000, grantee.pubkey())
    );
}

#[test]
fn grantee_withdraws_provider_bond_through_grant() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    set_clock(&mut svm, 1_000);
    let owner = funded_keypair(&mut svm);
    let grantee = funded_keypair(&mut svm);
    let rent_payer = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 5_000);
    send(
        &mut svm,
        &[ix_provider::open_provider_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            cfg,
            mint,
            vault,
            owner_token,
            [7u8; 32],
            vec![],
            1_000,
        )],
        &[&owner, &rent_payer],
    )
    .unwrap();
    send(
        &mut svm,
        &[ix_provider::close_provider_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            rent_payer.pubkey(),
            [7u8; 32],
            owner_token,
            mint,
        )],
        &[&owner, &rent_payer],
    )
    .unwrap();
    let grant = open_unscoped_grant(
        &mut svm,
        &owner,
        grantee.pubkey(),
        PERM_WITHDRAW_PROVIDER_BOND,
        0,
    );
    let unbonding_seconds = read_config(&svm).unbonding_seconds;
    set_clock(&mut svm, 1_000 + i64::from(unbonding_seconds));
    send(
        &mut svm,
        &[ix_provider::withdraw_provider_bond_ix_by(
            &grantee.pubkey(),
            Some(grant),
            &owner.pubkey(),
            [7u8; 32],
            owner_token,
            mint,
            rent_payer.pubkey(),
        )],
        &[&grantee],
    )
    .unwrap();
    assert_eq!(read_token_amount(&svm, &owner_token), 5_000);
    assert_solvent(&svm, 0);
}

#[test]
fn grantee_withdraws_provider_revenue_only_to_owner_token() {
    let genesis = genesis();
    let (mut svm, cfg, mint) = (genesis.svm, genesis.config, genesis.mint);
    let owner = funded_keypair(&mut svm);
    let grantee = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 0);
    let grantee_token = plant_ata(&mut svm, mint, &grantee.pubkey(), 0);
    let provider = plant_provider(
        &mut svm,
        cfg,
        &ProviderSpec {
            owner: owner.pubkey(),
            provider_id: [5u8; 32],
            revenue: 300,
            ..Default::default()
        },
    );
    let grant = open_unscoped_grant(
        &mut svm,
        &owner,
        grantee.pubkey(),
        PERM_WITHDRAW_PROVIDER_REVENUE,
        0,
    );
    let withdraw_revenue_ix = |token: Pubkey| {
        ix_provider::withdraw_provider_revenue_ix_by(
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
        &send_expect_err(&mut svm, &[withdraw_revenue_ix(grantee_token)], &[&grantee]),
        anchor_lang::error::ErrorCode::ConstraintTokenOwner,
    );
    svm.expire_blockhash();
    send(&mut svm, &[withdraw_revenue_ix(owner_token)], &[&grantee]).unwrap();
    assert_eq!(read_token_amount(&svm, &owner_token), 100);
    assert_eq!(read_provider(&svm, &provider).revenue, 200);
    assert_solvent(&svm, 0);
}

#[test]
fn grantee_withdraws_provider_balance_through_grant() {
    let genesis = genesis();
    let (mut svm, cfg, mint) = (genesis.svm, genesis.config, genesis.mint);
    let owner = funded_keypair(&mut svm);
    let grantee = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 0);
    let provider = plant_provider(
        &mut svm,
        cfg,
        &ProviderSpec {
            owner: owner.pubkey(),
            provider_id: [5u8; 32],
            balance: 200,
            ..Default::default()
        },
    );
    let grant = open_unscoped_grant(
        &mut svm,
        &owner,
        grantee.pubkey(),
        PERM_WITHDRAW_PROVIDER_BALANCE,
        0,
    );
    send(
        &mut svm,
        &[ix_provider::withdraw_provider_balance_ix_by(
            &grantee.pubkey(),
            Some(grant),
            &owner.pubkey(),
            [5u8; 32],
            owner_token,
            mint,
        )],
        &[&grantee],
    )
    .unwrap();
    assert_eq!(read_token_amount(&svm, &owner_token), 200);
    assert_eq!(read_provider(&svm, &provider).balance, 0);
    assert_solvent(&svm, 0);
}
