use dvpn::error::DvpnError;
use dvpn_test_support::ix_user;
use dvpn_test_support::*;
use litesvm::LiteSVM;
use solana_sdk::pubkey::Pubkey;
use solana_sdk::signature::Signer;

fn new_wallet() -> Pubkey {
    Pubkey::new_unique()
}

fn set_user_open_channels(svm: &mut LiteSVM, address: Pubkey, open_channels: u32) {
    let mut user_state = read_user(svm, &address);
    user_state.open_channels = open_channels;
    plant_account(svm, address, &user_state);
}

#[test]
fn withdraw_user_balance_idle_close() {
    let genesis = genesis_paused();
    let (mut svm, cfg, mint) = (genesis.svm, genesis.config, genesis.mint);
    let wallet = funded_keypair(&mut svm);
    let user_pda = plant_user(
        &mut svm,
        cfg,
        &UserSpec {
            owner: wallet.pubkey(),
            balance: 500,
            open_channels: 1,
        },
    );
    let owner_token = plant_ata(&mut svm, mint, &wallet.pubkey(), 0);
    send(
        &mut svm,
        &[ix_user::withdraw_user_balance_ix(
            &wallet.pubkey(),
            owner_token,
            mint,
        )],
        &[&wallet],
    )
    .unwrap();
    assert_eq!(read_token_amount(&svm, &owner_token), 500);
    assert_eq!(read_user(&svm, &user_pda).balance, 0);
    assert_solvent(&svm, 0);
    svm.expire_blockhash();
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_user::withdraw_user_balance_ix(
                &wallet.pubkey(),
                owner_token,
                mint,
            )],
            &[&wallet],
        ),
        DvpnError::BalanceZero,
    );
    set_user_open_channels(&mut svm, user_pda, 0);
    svm.expire_blockhash();
    send(
        &mut svm,
        &[ix_user::withdraw_user_balance_ix(
            &wallet.pubkey(),
            owner_token,
            mint,
        )],
        &[&wallet],
    )
    .unwrap();
    assert!(is_account_closed(&svm, &user_pda));
}

#[test]
fn withdraw_user_balance_pays_and_closes_when_idle_with_balance() {
    let genesis = genesis_paused();
    let (mut svm, cfg, mint) = (genesis.svm, genesis.config, genesis.mint);
    let wallet = funded_keypair(&mut svm);
    let user_pda = plant_user(
        &mut svm,
        cfg,
        &UserSpec {
            owner: wallet.pubkey(),
            balance: 700,
            open_channels: 0,
        },
    );
    let owner_token = plant_ata(&mut svm, mint, &wallet.pubkey(), 0);
    send(
        &mut svm,
        &[ix_user::withdraw_user_balance_ix(
            &wallet.pubkey(),
            owner_token,
            mint,
        )],
        &[&wallet],
    )
    .unwrap();
    assert_eq!(read_token_amount(&svm, &owner_token), 700);
    assert!(is_account_closed(&svm, &user_pda));
    assert_solvent(&svm, 0);
    plant_user(
        &mut svm,
        cfg,
        &UserSpec {
            owner: wallet.pubkey(),
            balance: 1,
            open_channels: 0,
        },
    );
    let foreign = plant_ata(&mut svm, mint, &new_wallet(), 0);
    assert!(
        send_expect_err(
            &mut svm,
            &[ix_user::withdraw_user_balance_ix(
                &wallet.pubkey(),
                foreign,
                mint
            )],
            &[&wallet],
        )
        .meta
        .logs
        .iter()
        .any(|line| line.contains("ConstraintTokenOwner"))
    );
}
