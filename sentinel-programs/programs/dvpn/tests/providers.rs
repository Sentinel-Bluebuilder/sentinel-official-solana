use dvpn::error::DvpnError;
use dvpn::state::{Provider, ProviderUnbonding};
use dvpn_core::enums::ListingStatus;
use dvpn_core::seeds::find_provider_unbonding;
use dvpn_test_support::*;
use dvpn_test_support::{ix_governance, ix_provider};
use litesvm::LiteSVM;
use solana_sdk::pubkey::Pubkey;
use solana_sdk::signature::Signer;

fn set_provider_fields(svm: &mut LiteSVM, address: Pubkey, mutate: impl FnOnce(&mut Provider)) {
    let mut provider_state = read_provider(svm, &address);
    mutate(&mut provider_state);
    plant_account(svm, address, &provider_state);
}

#[test]
fn open_provider_succeeds_delisted() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    let owner = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 20_000);
    let rent_payer = funded_keypair(&mut svm);
    let provider_id = [8u8; 32];
    send(
        &mut svm,
        &[ix_provider::open_provider_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            cfg,
            mint,
            vault,
            owner_token,
            provider_id,
            b"https://p".to_vec(),
            10_000,
        )],
        &[&owner, &rent_payer],
    )
    .unwrap();
    let provider_state = read_provider(&svm, &find_provider_address(owner.pubkey(), provider_id));
    assert_eq!(provider_state.status, ListingStatus::Delisted as u8);
    assert_eq!(provider_state.bond, 10_000);
    assert_eq!(provider_state.revenue, 0);
    assert_eq!(provider_state.fees_accrued, 0);
    assert_eq!(provider_state.balance, 0);
    assert_eq!(provider_state.open_plans, 0);
    assert_eq!(provider_state.open_leases, 0);
    assert_solvent(&svm, 0);
}

#[test]
fn provider_list_delist_and_bounds() {
    let genesis = genesis_paused();
    let (mut svm, cfg, mint, vault, authority) = (
        genesis.svm,
        genesis.config,
        genesis.mint,
        genesis.vault,
        genesis.authority,
    );
    let owner = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 20_000);
    let rent_payer = funded_keypair(&mut svm);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_provider::open_provider_ix(
                &owner.pubkey(),
                &rent_payer.pubkey(),
                cfg,
                mint,
                vault,
                owner_token,
                [1u8; 32],
                vec![],
                10_000,
            )],
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
            &[ix_provider::open_provider_ix(
                &owner.pubkey(),
                &rent_payer.pubkey(),
                cfg,
                mint,
                vault,
                owner_token,
                [1u8; 32],
                vec![],
                999,
            )],
            &[&owner, &rent_payer],
        ),
        DvpnError::BondBelowMinimum,
    );
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_provider::open_provider_ix(
                &owner.pubkey(),
                &rent_payer.pubkey(),
                cfg,
                mint,
                vault,
                owner_token,
                [1u8; 32],
                vec![0u8; 129],
                10_000,
            )],
            &[&owner, &rent_payer],
        ),
        DvpnError::UriTooLong,
    );
    svm.expire_blockhash();
    send(
        &mut svm,
        &[ix_provider::open_provider_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            cfg,
            mint,
            vault,
            owner_token,
            [1u8; 32],
            vec![],
            10_000,
        )],
        &[&owner, &rent_payer],
    )
    .unwrap();
    send(
        &mut svm,
        &[ix_provider::list_provider_ix(&owner.pubkey(), [1u8; 32])],
        &[&owner],
    )
    .unwrap();
    assert_eq!(
        read_provider(&svm, &find_provider_address(owner.pubkey(), [1u8; 32])).status,
        1
    );
    svm.expire_blockhash();
    send(
        &mut svm,
        &[ix_provider::list_provider_ix(&owner.pubkey(), [1u8; 32])],
        &[&owner],
    )
    .unwrap();
    send(
        &mut svm,
        &[ix_provider::delist_provider_ix(&owner.pubkey(), [1u8; 32])],
        &[&owner],
    )
    .unwrap();
    assert_eq!(
        read_provider(&svm, &find_provider_address(owner.pubkey(), [1u8; 32])).status,
        0
    );

    send(
        &mut svm,
        &[ix_provider::update_provider_ix(
            &owner.pubkey(),
            [1u8; 32],
            None,
        )],
        &[&owner],
    )
    .unwrap();
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_provider::update_provider_ix(
                &owner.pubkey(),
                [1u8; 32],
                Some(vec![0u8; 129]),
            )],
            &[&owner],
        ),
        DvpnError::UriTooLong,
    );
}

#[test]
fn open_provider_blocked_by_parked_bond() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    let owner = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 20_000);
    let rent_payer = funded_keypair(&mut svm);
    let provider_id = [4u8; 32];

    let (unbonding_address, unbonding_bump) =
        find_provider_unbonding(&dvpn::ID, &owner.pubkey(), &provider_id);
    plant_account(
        &mut svm,
        unbonding_address,
        &ProviderUnbonding {
            provider_id,
            owner: owner.pubkey(),
            bond: 500,
            closed_at: 0,
            rent_recipient: owner.pubkey(),
            bump: unbonding_bump,
            reserved: [0u8; 32],
        },
    );
    credit_vault(&mut svm, cfg, 500);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_provider::open_provider_ix(
                &owner.pubkey(),
                &rent_payer.pubkey(),
                cfg,
                mint,
                vault,
                owner_token,
                provider_id,
                vec![],
                10_000,
            )],
            &[&owner, &rent_payer],
        ),
        DvpnError::BondPending,
    );
    assert_solvent(&svm, 0);
}

#[test]
fn provider_pool_withdrawals() {
    let genesis = genesis();
    let (mut svm, cfg, mint) = (genesis.svm, genesis.config, genesis.mint);
    let owner = funded_keypair(&mut svm);
    let provider_id = [3u8; 32];
    plant_provider(
        &mut svm,
        cfg,
        &ProviderSpec {
            owner: owner.pubkey(),
            provider_id,
            revenue: 100,
            balance: 40,
            bond: 10_000,
            rent_recipient: owner.pubkey(),
            ..ProviderSpec::default()
        },
    );
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 0);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_provider::withdraw_provider_revenue_ix(
                &owner.pubkey(),
                provider_id,
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
            &[ix_provider::withdraw_provider_revenue_ix(
                &owner.pubkey(),
                provider_id,
                owner_token,
                101,
                mint,
            )],
            &[&owner],
        ),
        DvpnError::InsufficientRevenue,
    );
    send(
        &mut svm,
        &[ix_provider::withdraw_provider_revenue_ix(
            &owner.pubkey(),
            provider_id,
            owner_token,
            60,
            mint,
        )],
        &[&owner],
    )
    .unwrap();
    assert_eq!(
        read_provider(&svm, &find_provider_address(owner.pubkey(), provider_id)).revenue,
        40
    );
    send(
        &mut svm,
        &[ix_provider::withdraw_provider_balance_ix(
            &owner.pubkey(),
            provider_id,
            owner_token,
            mint,
        )],
        &[&owner],
    )
    .unwrap();
    assert_eq!(
        read_provider(&svm, &find_provider_address(owner.pubkey(), provider_id)).balance,
        0
    );
    svm.expire_blockhash();
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_provider::withdraw_provider_balance_ix(
                &owner.pubkey(),
                provider_id,
                owner_token,
                mint,
            )],
            &[&owner],
        ),
        DvpnError::BalanceZero,
    );
    assert_eq!(read_token_amount(&svm, &owner_token), 100);
    assert_solvent(&svm, 0);
}

#[test]
fn close_provider_preconditions_and_bond() {
    let genesis = genesis();
    let (mut svm, cfg, mint) = (genesis.svm, genesis.config, genesis.mint);
    let owner = funded_keypair(&mut svm);
    let provider_id = [6u8; 32];
    plant_provider(
        &mut svm,
        cfg,
        &ProviderSpec {
            owner: owner.pubkey(),
            provider_id,
            open_plans: 1,
            bond: 10_000,
            rent_recipient: owner.pubkey(),
            ..ProviderSpec::default()
        },
    );
    let rent_payer = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 0);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_provider::close_provider_ix(
                &owner.pubkey(),
                &rent_payer.pubkey(),
                owner.pubkey(),
                provider_id,
                owner_token,
                mint,
            )],
            &[&owner, &rent_payer],
        ),
        DvpnError::OpenPlansRemain,
    );
    set_provider_fields(
        &mut svm,
        find_provider_address(owner.pubkey(), provider_id),
        |provider_state| {
            provider_state.open_plans = 0;
            provider_state.open_leases = 1;
        },
    );
    svm.expire_blockhash();
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_provider::close_provider_ix(
                &owner.pubkey(),
                &rent_payer.pubkey(),
                owner.pubkey(),
                provider_id,
                owner_token,
                mint,
            )],
            &[&owner, &rent_payer],
        ),
        DvpnError::OpenLeasesRemain,
    );
    set_provider_fields(
        &mut svm,
        find_provider_address(owner.pubkey(), provider_id),
        |provider_state| {
            provider_state.open_leases = 0;
            provider_state.revenue = 1;
            provider_state.fees_accrued = 1;
            provider_state.balance = 1;
        },
    );
    credit_vault(&mut svm, cfg, 3);
    set_clock(&mut svm, 1000);
    svm.expire_blockhash();
    send(
        &mut svm,
        &[ix_provider::close_provider_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            owner.pubkey(),
            provider_id,
            owner_token,
            mint,
        )],
        &[&owner, &rent_payer],
    )
    .unwrap();
    assert_eq!(read_token_amount(&svm, &owner_token), 2);
    assert_eq!(read_config(&svm).fees_accrued, 1);
    assert_solvent(&svm, 0);
    assert_eq!(
        read_provider_unbonding(
            &svm,
            &find_provider_unbonding_address(owner.pubkey(), provider_id)
        )
        .bond,
        10_000
    );
    assert!(is_account_closed(
        &svm,
        &find_provider_address(owner.pubkey(), provider_id)
    ));

    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 0);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_provider::withdraw_provider_bond_ix(
                &owner.pubkey(),
                provider_id,
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
    send(
        &mut svm,
        &[ix_provider::withdraw_provider_bond_ix(
            &owner.pubkey(),
            provider_id,
            owner_token,
            mint,
            rent_payer.pubkey(),
        )],
        &[&owner],
    )
    .unwrap();
    assert_eq!(read_token_amount(&svm, &owner_token), 10_000);
    assert!(is_account_closed(
        &svm,
        &find_provider_unbonding_address(owner.pubkey(), provider_id)
    ));
    assert_solvent(&svm, 0);
}

#[test]
fn withdraw_provider_bond_non_owner_is_unauthorized() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    let OpenedProvider {
        owner,
        rent_payer,
        provider_id,
    } = open_default_provider(&mut svm, cfg, mint, vault);
    let owner_token = plant_ata(&mut svm, mint, &owner.pubkey(), 0);
    set_clock(&mut svm, 1000);
    send(
        &mut svm,
        &[ix_provider::close_provider_ix(
            &owner.pubkey(),
            &rent_payer.pubkey(),
            rent_payer.pubkey(),
            provider_id,
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
            &[ix_provider::withdraw_provider_bond_ix_by(
                &stranger.pubkey(),
                None,
                &owner.pubkey(),
                provider_id,
                owner_token,
                mint,
                rent_payer.pubkey(),
            )],
            &[&stranger],
        ),
        DvpnError::Unauthorized,
    );
}
