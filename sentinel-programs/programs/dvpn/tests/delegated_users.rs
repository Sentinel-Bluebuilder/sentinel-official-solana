#![allow(clippy::unwrap_used)]

use dvpn::error::DvpnError;
use dvpn_core::permissions::*;
use dvpn_core::seeds::find_user;
use dvpn_test_support::*;
use dvpn_test_support::{ix_grant, ix_user};
use solana_sdk::signature::Signer;

#[test]
fn grantee_withdraws_user_balance_only_to_owner_token_and_only_unscoped() {
    let genesis = genesis();
    let (mut svm, cfg, mint) = (genesis.svm, genesis.config, genesis.mint);
    let owner = funded_keypair(&mut svm);
    let grantee = funded_keypair(&mut svm);
    let rent_payer = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 0);
    let grantee_token = plant_ata(&mut svm, mint, &grantee.pubkey(), 0);
    let user = plant_user(
        &mut svm,
        cfg,
        &UserSpec {
            owner: owner.pubkey(),
            balance: 400,
            open_channels: 1,
        },
    );
    send(
        &mut svm,
        &[ix_grant::open_grant_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            grantee.pubkey(),
            user,
            PERM_WITHDRAW_USER_BALANCE,
            0,
            0,
            rent_payer.pubkey(),
        )],
        &[&owner, &rent_payer],
    )
    .unwrap();
    let scoped = find_grant_address(owner.pubkey(), grantee.pubkey(), user);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_user::withdraw_user_balance_ix_by(
                &grantee.pubkey(),
                Some(scoped),
                &owner.pubkey(),
                owner.pubkey(),
                owner_token,
                mint,
            )],
            &[&grantee],
        ),
        DvpnError::GrantTargetMismatch,
    );
    let grant = open_unscoped_grant(
        &mut svm,
        &owner,
        grantee.pubkey(),
        PERM_WITHDRAW_USER_BALANCE,
        0,
    );
    assert_anchor_error(
        &send_expect_err(
            &mut svm,
            &[ix_user::withdraw_user_balance_ix_by(
                &grantee.pubkey(),
                Some(grant),
                &owner.pubkey(),
                owner.pubkey(),
                grantee_token,
                mint,
            )],
            &[&grantee],
        ),
        anchor_lang::error::ErrorCode::ConstraintTokenOwner,
    );
    svm.expire_blockhash();
    send(
        &mut svm,
        &[ix_user::withdraw_user_balance_ix_by(
            &grantee.pubkey(),
            Some(grant),
            &owner.pubkey(),
            owner.pubkey(),
            owner_token,
            mint,
        )],
        &[&grantee],
    )
    .unwrap();
    assert_eq!(read_token_amount(&svm, &owner_token), 400);
    assert_eq!(
        read_user(&svm, &find_user(&dvpn::ID, &owner.pubkey()).0).balance,
        0
    );
    assert_solvent(&svm, 0);
}
