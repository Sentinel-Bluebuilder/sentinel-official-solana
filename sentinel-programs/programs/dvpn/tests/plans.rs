use dvpn::error::DvpnError;
use dvpn_core::constants::PAUSE_PLAN_OPENS;
use dvpn_core::permissions::*;
use dvpn_core::seeds::find_grant;
use dvpn_test_support::ix_governance;
use dvpn_test_support::*;
use dvpn_test_support::{ix_grant, ix_plan};
use litesvm::LiteSVM;
use solana_sdk::pubkey::Pubkey;
use solana_sdk::signature::{Keypair, Signer};

#[test]
fn plan_lifecycle() {
    let mut genesis = genesis();
    let OpenedProvider {
        owner, provider_id, ..
    } = open_default_provider(
        &mut genesis.svm,
        genesis.config,
        genesis.mint,
        genesis.vault,
    );
    let mut svm = genesis.svm;
    let plan_id = [9u8; 32];
    let provider = find_provider_address(owner.pubkey(), provider_id);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_plan::open_plan_ix(
                &owner.pubkey(),
                provider,
                plan_id,
                100,
                0,
                0,
                5,
            )],
            &[&owner],
        ),
        DvpnError::ParamOutOfRange,
    );
    send(
        &mut svm,
        &[ix_plan::open_plan_ix(
            &owner.pubkey(),
            provider,
            plan_id,
            100,
            2_592_000,
            0,
            5,
        )],
        &[&owner],
    )
    .unwrap();
    assert_eq!(
        read_plan(&svm, &find_plan_address(provider, plan_id)).status,
        0
    );
    assert_eq!(read_provider(&svm, &provider).open_plans, 1);
    send(
        &mut svm,
        &[ix_plan::list_plan_ix(&owner.pubkey(), provider, plan_id)],
        &[&owner],
    )
    .unwrap();
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_plan::close_plan_ix(&owner.pubkey(), provider, plan_id)],
            &[&owner],
        ),
        DvpnError::PlanNotDelisted,
    );
    send(
        &mut svm,
        &[ix_plan::delist_plan_ix(&owner.pubkey(), provider, plan_id)],
        &[&owner],
    )
    .unwrap();
    svm.expire_blockhash();
    send(
        &mut svm,
        &[ix_plan::close_plan_ix(&owner.pubkey(), provider, plan_id)],
        &[&owner],
    )
    .unwrap();
    assert_eq!(read_provider(&svm, &provider).open_plans, 0);

    send(
        &mut svm,
        &[ix_plan::open_plan_ix(
            &owner.pubkey(),
            provider,
            [10u8; 32],
            1,
            60,
            0,
            1,
        )],
        &[&owner],
    )
    .unwrap();
    send(
        &mut svm,
        &[ix_plan::update_plan_ix(
            &owner.pubkey(),
            provider,
            [10u8; 32],
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
fn open_plan_paused_by_its_own_bit() {
    let mut genesis = genesis();
    let OpenedProvider {
        owner, provider_id, ..
    } = open_default_provider(
        &mut genesis.svm,
        genesis.config,
        genesis.mint,
        genesis.vault,
    );
    let (mut svm, authority) = (genesis.svm, genesis.authority);
    let provider = find_provider_address(owner.pubkey(), provider_id);
    send(
        &mut svm,
        &[ix_governance::set_paused_mask_ix(
            &authority.pubkey(),
            PAUSE_PLAN_OPENS,
        )],
        &[&authority],
    )
    .unwrap();
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_plan::open_plan_ix(
                &owner.pubkey(),
                provider,
                [1u8; 32],
                100,
                60,
                0,
                1,
            )],
            &[&owner],
        ),
        DvpnError::OperationPaused,
    );
    send(
        &mut svm,
        &[ix_governance::set_paused_mask_ix(&authority.pubkey(), 0)],
        &[&authority],
    )
    .unwrap();
    svm.expire_blockhash();
    send(
        &mut svm,
        &[ix_plan::open_plan_ix(
            &owner.pubkey(),
            provider,
            [1u8; 32],
            100,
            60,
            0,
            1,
        )],
        &[&owner],
    )
    .unwrap();
}

#[test]
fn grantee_signed_plan_ops_succeed() {
    let mut genesis = genesis();
    let OpenedProvider {
        owner, provider_id, ..
    } = open_default_provider(
        &mut genesis.svm,
        genesis.config,
        genesis.mint,
        genesis.vault,
    );
    let mut svm = genesis.svm;
    let provider = find_provider_address(owner.pubkey(), provider_id);
    let grantee = Keypair::new();
    let rent_payer = funded_keypair(&mut svm);
    send(
        &mut svm,
        &[ix_grant::open_grant_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            grantee.pubkey(),
            Pubkey::default(),
            PERM_OPEN_PLAN | PERM_UPDATE_PLAN | PERM_LIST_PLAN | PERM_DELIST_PLAN | PERM_CLOSE_PLAN,
            0,
            0,
            rent_payer.pubkey(),
        )],
        &[&owner, &rent_payer],
    )
    .unwrap();
    svm.airdrop(&grantee.pubkey(), 10_000_000_000).unwrap();
    let plan_id = [3u8; 32];
    send(
        &mut svm,
        &[ix_plan::open_plan_ix_as_grantee(
            &grantee.pubkey(),
            &find_grant_address(owner.pubkey(), grantee.pubkey(), Pubkey::default()),
            provider,
            plan_id,
            50,
            120,
            0,
            2,
        )],
        &[&grantee],
    )
    .unwrap();
    send(
        &mut svm,
        &[ix_plan::list_plan_ix_as_grantee(
            &grantee.pubkey(),
            &find_grant_address(owner.pubkey(), grantee.pubkey(), Pubkey::default()),
            provider,
            plan_id,
        )],
        &[&grantee],
    )
    .unwrap();
    assert_eq!(
        read_plan(&svm, &find_plan_address(provider, plan_id)).status,
        1
    );
    send(
        &mut svm,
        &[ix_plan::update_plan_ix_as_grantee(
            &grantee.pubkey(),
            &find_grant_address(owner.pubkey(), grantee.pubkey(), Pubkey::default()),
            provider,
            plan_id,
            Some(75),
            None,
            None,
            None,
        )],
        &[&grantee],
    )
    .unwrap();
    assert_eq!(
        read_plan(&svm, &find_plan_address(provider, plan_id)).price,
        75
    );
    send(
        &mut svm,
        &[ix_plan::delist_plan_ix_as_grantee(
            &grantee.pubkey(),
            &find_grant_address(owner.pubkey(), grantee.pubkey(), Pubkey::default()),
            provider,
            plan_id,
        )],
        &[&grantee],
    )
    .unwrap();
    assert_eq!(
        read_plan(&svm, &find_plan_address(provider, plan_id)).status,
        0
    );
    send(
        &mut svm,
        &[ix_plan::close_plan_ix_as_grantee(
            &grantee.pubkey(),
            &find_grant_address(owner.pubkey(), grantee.pubkey(), Pubkey::default()),
            provider,
            plan_id,
            grantee.pubkey(),
        )],
        &[&grantee],
    )
    .unwrap();
    assert_eq!(read_provider(&svm, &provider).open_plans, 0);
    assert_solvent(&svm, 0);
}

#[test]
fn wrong_provider_account_fails_constraint_has_one() {
    let mut genesis = genesis();
    let OpenedProvider {
        owner, provider_id, ..
    } = open_default_provider(
        &mut genesis.svm,
        genesis.config,
        genesis.mint,
        genesis.vault,
    );
    let OpenedProvider {
        owner: other_owner,
        provider_id: other_provider_id,
        ..
    } = open_default_provider(
        &mut genesis.svm,
        genesis.config,
        genesis.mint,
        genesis.vault,
    );
    let mut svm = genesis.svm;
    let provider = find_provider_address(owner.pubkey(), provider_id);
    let other_provider = find_provider_address(other_owner.pubkey(), other_provider_id);
    let plan_id = [4u8; 32];
    send(
        &mut svm,
        &[ix_plan::open_plan_ix(
            &owner.pubkey(),
            provider,
            plan_id,
            100,
            60,
            0,
            1,
        )],
        &[&owner],
    )
    .unwrap();
    let plan = find_plan_address(provider, plan_id);
    assert_anchor_error(
        &send_expect_err(
            &mut svm,
            &[ix_plan::list_plan_ix_by(
                &owner.pubkey(),
                None,
                other_provider,
                plan,
            )],
            &[&owner],
        ),
        anchor_lang::error::ErrorCode::ConstraintHasOne,
    );
    assert_anchor_error(
        &send_expect_err(
            &mut svm,
            &[ix_plan::delist_plan_ix_by(
                &owner.pubkey(),
                None,
                other_provider,
                plan,
            )],
            &[&owner],
        ),
        anchor_lang::error::ErrorCode::ConstraintHasOne,
    );
    assert_anchor_error(
        &send_expect_err(
            &mut svm,
            &[ix_plan::update_plan_ix_by(
                &owner.pubkey(),
                None,
                other_provider,
                plan,
                None,
                None,
                None,
                None,
            )],
            &[&owner],
        ),
        anchor_lang::error::ErrorCode::ConstraintHasOne,
    );
    assert_anchor_error(
        &send_expect_err(
            &mut svm,
            &[ix_plan::close_plan_ix_by(
                &owner.pubkey(),
                None,
                other_provider,
                plan,
                owner.pubkey(),
            )],
            &[&owner],
        ),
        anchor_lang::error::ErrorCode::ConstraintHasOne,
    );
}

#[test]
fn plan_ops_accept_grant_scoped_to_provider_or_plan() {
    let mut genesis = genesis();
    let OpenedProvider {
        owner, provider_id, ..
    } = open_default_provider(
        &mut genesis.svm,
        genesis.config,
        genesis.mint,
        genesis.vault,
    );
    let mut svm = genesis.svm;
    let provider = find_provider_address(owner.pubkey(), provider_id);
    let grantee = Keypair::new();
    let grantee_key = grantee.pubkey();
    svm.airdrop(&grantee_key, 10_000_000_000).unwrap();
    let rent_payer = funded_keypair(&mut svm);
    let grant = |svm: &mut LiteSVM, target, perms| {
        let instruction = ix_grant::open_grant_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            grantee_key,
            target,
            perms,
            0,
            0,
            rent_payer.pubkey(),
        );
        send(svm, &[instruction], &[&owner, &rent_payer]).unwrap();
        find_grant(&dvpn::ID, &owner.pubkey(), &grantee_key, &target).0
    };
    let by_provider = grant(&mut svm, provider, PERM_OPEN_PLAN | PERM_LIST_PLAN);
    let open = |plan_rent_recipient| {
        ix_plan::open_plan_ix_by(
            &grantee_key,
            Some(by_provider),
            &rent_payer.pubkey(),
            provider,
            [9u8; 32],
            50,
            3_600,
            1,
            1,
            plan_rent_recipient,
        )
    };
    let err = send_expect_err(&mut svm, &[open(owner.pubkey())], &[&grantee, &rent_payer]);
    assert_dvpn_error(&err, DvpnError::InvalidRentRecipient);
    send(&mut svm, &[open(grantee_key)], &[&grantee, &rent_payer]).unwrap();
    let plan = find_plan_address(provider, [9u8; 32]);
    assert_eq!(read_plan(&svm, &plan).rent_recipient, grantee_key);
    let by_plan = grant(&mut svm, plan, PERM_DELIST_PLAN);
    let list = ix_plan::list_plan_ix_by(&grantee_key, Some(by_provider), provider, plan);
    send(&mut svm, &[list], &[&grantee]).unwrap();
    let delist = |grant| ix_plan::delist_plan_ix_by(&grantee_key, Some(grant), provider, plan);
    let err = send_expect_err(&mut svm, &[delist(by_provider)], &[&grantee]);
    assert_dvpn_error(&err, DvpnError::PermissionNotGranted);
    svm.expire_blockhash();
    send(&mut svm, &[delist(by_plan)], &[&grantee]).unwrap();
    assert_eq!(read_plan(&svm, &plan).status, 0);
}
