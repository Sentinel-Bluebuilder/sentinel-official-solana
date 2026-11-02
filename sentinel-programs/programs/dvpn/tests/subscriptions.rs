use dvpn::error::DvpnError;
use dvpn_core::constants::PAUSE_SUBSCRIPTION_OPENS;
use dvpn_core::permissions::PERM_CLOSE_FREE_SUBSCRIPTION;
use dvpn_core::seeds::find_grant;
use dvpn_test_support::ix_governance;
use dvpn_test_support::*;
use dvpn_test_support::{ix_grant, ix_plan, ix_provider, ix_subscription};
use litesvm::LiteSVM;
use solana_sdk::pubkey::Pubkey;
use solana_sdk::signature::{Keypair, Signer};

fn new_wallet() -> Pubkey {
    Pubkey::new_unique()
}

fn listed_priced_plan(
    svm: &mut LiteSVM,
    config: Pubkey,
    mint: Pubkey,
    vault: Pubkey,
    price: u64,
    duration_seconds: u32,
) -> (Keypair, [u8; 32], [u8; 32], Pubkey) {
    let provider_owner = funded_keypair(svm);
    let owner_token = plant_ata(svm, mint, &provider_owner.pubkey(), 20_000);
    let rent_payer = funded_keypair(svm);
    let provider_id = Pubkey::new_unique().to_bytes();
    send(
        svm,
        &[ix_provider::open_provider_ix(
            &provider_owner.pubkey(),
            &rent_payer.pubkey(),
            config,
            mint,
            vault,
            owner_token,
            provider_id,
            vec![],
            1_000,
        )],
        &[&provider_owner, &rent_payer],
    )
    .unwrap();
    let provider = find_provider_address(provider_owner.pubkey(), provider_id);
    send(
        svm,
        &[ix_provider::list_provider_ix(
            &provider_owner.pubkey(),
            provider_id,
        )],
        &[&provider_owner],
    )
    .unwrap();
    let plan_id = Pubkey::new_unique().to_bytes();
    send(
        svm,
        &[ix_plan::open_plan_ix(
            &provider_owner.pubkey(),
            provider,
            plan_id,
            price,
            duration_seconds,
            0,
            5,
        )],
        &[&provider_owner],
    )
    .unwrap();
    send(
        svm,
        &[ix_plan::list_plan_ix(
            &provider_owner.pubkey(),
            provider,
            plan_id,
        )],
        &[&provider_owner],
    )
    .unwrap();
    (
        provider_owner,
        provider_id,
        plan_id,
        find_plan_address(provider, plan_id),
    )
}

fn listed_zero_plan(
    svm: &mut LiteSVM,
    config: Pubkey,
    mint: Pubkey,
    vault: Pubkey,
) -> (Keypair, [u8; 32], [u8; 32], Pubkey) {
    listed_priced_plan(svm, config, mint, vault, 0, 2_592_000)
}

#[test]
fn open_subscription_priced_pays_and_splits_fee() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault, authority) = (
        genesis.svm,
        genesis.config,
        genesis.mint,
        genesis.vault,
        genesis.authority,
    );
    send(
        &mut svm,
        &[ix_governance::set_params_ix(
            &authority.pubkey(),
            params_with_fee_bps(200),
        )],
        &[&authority],
    )
    .unwrap();
    let (provider_owner, provider_id, _plan_id, plan) =
        listed_priced_plan(&mut svm, cfg, mint, vault, 10_000, 2_592_000);
    let provider = find_provider_address(provider_owner.pubkey(), provider_id);
    let subscriber = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &subscriber.pubkey(), 1_000_000);
    let rent_payer = funded_keypair(&mut svm);
    set_clock(&mut svm, 5000);
    send(
        &mut svm,
        &[ix_subscription::open_subscription_ix_with_price(
            &subscriber.pubkey(),
            &rent_payer.pubkey(),
            subscriber.pubkey(),
            provider,
            plan,
            owner_token,
            cfg,
            mint,
            vault,
            10_000,
            2_592_000,
            0,
            5,
            None,
            rent_payer.pubkey(),
        )],
        &[&subscriber, &rent_payer],
    )
    .unwrap();
    let subscription_state =
        read_subscription(&svm, &find_subscription_address(subscriber.pubkey(), plan));
    assert_eq!(subscription_state.price, 10_000);
    assert_eq!(subscription_state.expires_at, 5000 + 2_592_000);
    let provider_state = read_provider(&svm, &provider);
    assert_eq!(provider_state.revenue, 9_800);
    assert_eq!(provider_state.fees_accrued, 200);
    assert_solvent(&svm, 0);

    let other = new_wallet();
    let other_token = plant_ata(&mut svm, mint, &other, 0);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_subscription::open_subscription_ix_with_price(
                &provider_owner.pubkey(),
                &rent_payer.pubkey(),
                other,
                provider,
                plan,
                other_token,
                cfg,
                mint,
                vault,
                10_000,
                2_592_000,
                0,
                5,
                None,
                rent_payer.pubkey(),
            )],
            &[&provider_owner, &rent_payer],
        ),
        DvpnError::Unauthorized,
    );

    let subscriber_2 = funded_keypair(&mut svm);
    let subscriber_2_token = plant_ata(&mut svm, mint, &subscriber_2.pubkey(), 1_000_000);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_subscription::open_subscription_ix_with_price(
                &subscriber_2.pubkey(),
                &rent_payer.pubkey(),
                subscriber_2.pubkey(),
                provider,
                plan,
                subscriber_2_token,
                cfg,
                mint,
                vault,
                9_999,
                2_592_000,
                0,
                5,
                None,
                rent_payer.pubkey(),
            )],
            &[&subscriber_2, &rent_payer],
        ),
        DvpnError::PlanTermsChanged,
    );

    let plan_terms = read_plan(&svm, &plan);

    let subscriber_3 = funded_keypair(&mut svm);
    let subscriber_3_token = plant_ata(&mut svm, mint, &subscriber_3.pubkey(), 1_000_000);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_subscription::open_subscription_ix_with_price(
                &subscriber_3.pubkey(),
                &rent_payer.pubkey(),
                subscriber_3.pubkey(),
                provider,
                plan,
                subscriber_3_token,
                cfg,
                mint,
                vault,
                plan_terms.price,
                plan_terms.duration_seconds - 1,
                plan_terms.data_cap_gb,
                plan_terms.max_connections,
                None,
                rent_payer.pubkey(),
            )],
            &[&subscriber_3, &rent_payer],
        ),
        DvpnError::PlanTermsChanged,
    );

    let subscriber_4 = funded_keypair(&mut svm);
    let subscriber_4_token = plant_ata(&mut svm, mint, &subscriber_4.pubkey(), 1_000_000);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_subscription::open_subscription_ix_with_price(
                &subscriber_4.pubkey(),
                &rent_payer.pubkey(),
                subscriber_4.pubkey(),
                provider,
                plan,
                subscriber_4_token,
                cfg,
                mint,
                vault,
                plan_terms.price,
                plan_terms.duration_seconds,
                plan_terms.data_cap_gb + 1,
                plan_terms.max_connections,
                None,
                rent_payer.pubkey(),
            )],
            &[&subscriber_4, &rent_payer],
        ),
        DvpnError::PlanTermsChanged,
    );

    let subscriber_5 = funded_keypair(&mut svm);
    let subscriber_5_token = plant_ata(&mut svm, mint, &subscriber_5.pubkey(), 1_000_000);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_subscription::open_subscription_ix_with_price(
                &subscriber_5.pubkey(),
                &rent_payer.pubkey(),
                subscriber_5.pubkey(),
                provider,
                plan,
                subscriber_5_token,
                cfg,
                mint,
                vault,
                plan_terms.price,
                plan_terms.duration_seconds,
                plan_terms.data_cap_gb,
                plan_terms.max_connections - 1,
                None,
                rent_payer.pubkey(),
            )],
            &[&subscriber_5, &rent_payer],
        ),
        DvpnError::PlanTermsChanged,
    );

    let err = send_expect_err(
        &mut svm,
        &[
            ix_subscription::open_subscription_ix_without_token_accounts(
                &subscriber_2.pubkey(),
                &rent_payer.pubkey(),
                subscriber_2.pubkey(),
                provider,
                plan,
                cfg,
                10_000,
                2_592_000,
                0,
                5,
                rent_payer.pubkey(),
            ),
        ],
        &[&subscriber_2, &rent_payer],
    );
    assert_anchor_error(&err, anchor_lang::error::ErrorCode::ConstraintAccountIsNone);
}

#[test]
fn open_subscription_zero_priced_by_provider_admin() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    let (provider_owner, provider_id, _plan_id, plan) =
        listed_zero_plan(&mut svm, cfg, mint, vault);
    let provider = find_provider_address(provider_owner.pubkey(), provider_id);
    let rent_payer = funded_keypair(&mut svm);
    let wallet = new_wallet();
    let subscription = find_subscription_address(wallet, plan);
    let meta = send(
        &mut svm,
        &[ix_subscription::open_subscription_ix_free(
            &provider_owner.pubkey(),
            &rent_payer.pubkey(),
            wallet,
            provider,
            plan,
            2_592_000,
            0,
            5,
            None,
            provider_owner.pubkey(),
        )],
        &[&provider_owner, &rent_payer],
    )
    .unwrap();
    let stored = read_subscription(&svm, &subscription);
    assert_eq!(
        (stored.price, stored.rent_recipient),
        (0, provider_owner.pubkey())
    );
    let provider_state = read_provider(&svm, &provider);
    assert_event(
        &meta,
        &dvpn::events::SubscriptionOpened {
            subscription,
            owner: wallet,
            plan,
            provider,
            price: 0,
            fee: 0,
            expires_at: stored.expires_at,
            data_cap_gb: 0,
            max_connections: 5,
            subscription_rent_recipient: provider_owner.pubkey(),
            provider_revenue: provider_state.revenue,
            provider_fees_accrued: provider_state.fees_accrued,
        },
    );

    let stranger = funded_keypair(&mut svm);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_subscription::open_subscription_ix_free(
                &stranger.pubkey(),
                &rent_payer.pubkey(),
                stranger.pubkey(),
                provider,
                plan,
                2_592_000,
                0,
                5,
                None,
                rent_payer.pubkey(),
            )],
            &[&stranger, &rent_payer],
        ),
        DvpnError::Unauthorized,
    );
}

#[test]
fn open_fails_paused_under_subscription_bit() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault, authority) = (
        genesis.svm,
        genesis.config,
        genesis.mint,
        genesis.vault,
        genesis.authority,
    );
    let (provider_owner, provider_id, _plan_id, plan) =
        listed_priced_plan(&mut svm, cfg, mint, vault, 1_000, 100);
    let provider = find_provider_address(provider_owner.pubkey(), provider_id);
    let subscriber = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &subscriber.pubkey(), 1_000_000);
    let rent_payer = funded_keypair(&mut svm);

    send(
        &mut svm,
        &[ix_governance::set_paused_mask_ix(
            &authority.pubkey(),
            PAUSE_SUBSCRIPTION_OPENS,
        )],
        &[&authority],
    )
    .unwrap();
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_subscription::open_subscription_ix_with_price(
                &subscriber.pubkey(),
                &rent_payer.pubkey(),
                subscriber.pubkey(),
                provider,
                plan,
                owner_token,
                cfg,
                mint,
                vault,
                1_000,
                100,
                0,
                5,
                None,
                rent_payer.pubkey(),
            )],
            &[&subscriber, &rent_payer],
        ),
        DvpnError::OperationPaused,
    );

    let (zero_price_owner, zero_price_provider_id, _zero_price_plan_id, zero_price_plan) =
        listed_zero_plan(&mut svm, cfg, mint, vault);
    let zero_price_provider =
        find_provider_address(zero_price_owner.pubkey(), zero_price_provider_id);
    let wallet = new_wallet();
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_subscription::open_subscription_ix_free(
                &zero_price_owner.pubkey(),
                &rent_payer.pubkey(),
                wallet,
                zero_price_provider,
                zero_price_plan,
                2_592_000,
                0,
                5,
                None,
                rent_payer.pubkey(),
            )],
            &[&zero_price_owner, &rent_payer],
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
        &[ix_subscription::open_subscription_ix_with_price(
            &subscriber.pubkey(),
            &rent_payer.pubkey(),
            subscriber.pubkey(),
            provider,
            plan,
            owner_token,
            cfg,
            mint,
            vault,
            1_000,
            100,
            0,
            5,
            None,
            rent_payer.pubkey(),
        )],
        &[&subscriber, &rent_payer],
    )
    .unwrap();
}

#[test]
fn close_subscription_rules() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    let rent_payer = funded_keypair(&mut svm);

    let (provider_owner, provider_id, _plan_id, zero_price_plan) =
        listed_zero_plan(&mut svm, cfg, mint, vault);
    let provider = find_provider_address(provider_owner.pubkey(), provider_id);
    let wallet = new_wallet();
    set_clock(&mut svm, 1000);
    send(
        &mut svm,
        &[ix_subscription::open_subscription_ix_free(
            &provider_owner.pubkey(),
            &rent_payer.pubkey(),
            wallet,
            provider,
            zero_price_plan,
            2_592_000,
            0,
            5,
            None,
            rent_payer.pubkey(),
        )],
        &[&provider_owner, &rent_payer],
    )
    .unwrap();
    let subscription_account = find_subscription_address(wallet, zero_price_plan);
    send(
        &mut svm,
        &[ix_subscription::close_subscription_ix_with_provider(
            &provider_owner.pubkey(),
            subscription_account,
            provider,
            rent_payer.pubkey(),
            None,
        )],
        &[&provider_owner],
    )
    .unwrap();
    assert!(is_account_closed(&svm, &subscription_account));

    let (provider_owner_2, provider_id_2, _plan_id_2, plan_2) =
        listed_priced_plan(&mut svm, cfg, mint, vault, 10_000, 60);
    let provider_2 = find_provider_address(provider_owner_2.pubkey(), provider_id_2);
    let subscriber = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &subscriber.pubkey(), 1_000_000);
    set_clock(&mut svm, 2000);
    send(
        &mut svm,
        &[ix_subscription::open_subscription_ix_with_price(
            &subscriber.pubkey(),
            &rent_payer.pubkey(),
            subscriber.pubkey(),
            provider_2,
            plan_2,
            owner_token,
            cfg,
            mint,
            vault,
            10_000,
            60,
            0,
            5,
            None,
            rent_payer.pubkey(),
        )],
        &[&subscriber, &rent_payer],
    )
    .unwrap();
    let subscription_account_2 = find_subscription_address(subscriber.pubkey(), plan_2);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_subscription::close_subscription_ix_with_provider(
                &provider_owner_2.pubkey(),
                subscription_account_2,
                provider_2,
                rent_payer.pubkey(),
                None,
            )],
            &[&provider_owner_2],
        ),
        DvpnError::SubscriptionPriceNotZero,
    );
    let stranger = funded_keypair(&mut svm);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_subscription::close_subscription_ix(
                &stranger.pubkey(),
                subscription_account_2,
                rent_payer.pubkey(),
                None,
            )],
            &[&stranger],
        ),
        DvpnError::SubscriptionNotExpired,
    );

    send(
        &mut svm,
        &[ix_subscription::close_subscription_ix(
            &subscriber.pubkey(),
            subscription_account_2,
            rent_payer.pubkey(),
            None,
        )],
        &[&subscriber],
    )
    .unwrap();
    assert!(is_account_closed(&svm, &subscription_account_2));

    let subscriber_3 = funded_keypair(&mut svm);
    let subscriber_3_token = plant_ata(&mut svm, mint, &subscriber_3.pubkey(), 1_000_000);
    send(
        &mut svm,
        &[ix_subscription::open_subscription_ix_with_price(
            &subscriber_3.pubkey(),
            &rent_payer.pubkey(),
            subscriber_3.pubkey(),
            provider_2,
            plan_2,
            subscriber_3_token,
            cfg,
            mint,
            vault,
            10_000,
            60,
            0,
            5,
            None,
            rent_payer.pubkey(),
        )],
        &[&subscriber_3, &rent_payer],
    )
    .unwrap();
    let subscription_account_3 = find_subscription_address(subscriber_3.pubkey(), plan_2);
    set_clock(&mut svm, 2000 + 60);
    let stranger = funded_keypair(&mut svm);
    send(
        &mut svm,
        &[ix_subscription::close_subscription_ix(
            &stranger.pubkey(),
            subscription_account_3,
            rent_payer.pubkey(),
            None,
        )],
        &[&stranger],
    )
    .unwrap();
    assert!(is_account_closed(&svm, &subscription_account_3));
}

#[test]
fn provider_close_requires_owner_or_grantee() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    let (provider_owner, provider_id, _plan_id, zero_price_plan) =
        listed_zero_plan(&mut svm, cfg, mint, vault);
    let provider = find_provider_address(provider_owner.pubkey(), provider_id);
    let rent_payer = funded_keypair(&mut svm);
    let subscriber = funded_keypair(&mut svm);
    let subscriber_2 = funded_keypair(&mut svm);
    set_clock(&mut svm, 1_000);
    send(
        &mut svm,
        &[ix_subscription::open_subscription_ix_free(
            &provider_owner.pubkey(),
            &rent_payer.pubkey(),
            subscriber.pubkey(),
            provider,
            zero_price_plan,
            2_592_000,
            0,
            5,
            None,
            rent_payer.pubkey(),
        )],
        &[&provider_owner, &rent_payer],
    )
    .unwrap();
    send(
        &mut svm,
        &[ix_subscription::open_subscription_ix_free(
            &provider_owner.pubkey(),
            &rent_payer.pubkey(),
            subscriber_2.pubkey(),
            provider,
            zero_price_plan,
            2_592_000,
            0,
            5,
            None,
            rent_payer.pubkey(),
        )],
        &[&provider_owner, &rent_payer],
    )
    .unwrap();
    let subscription_account = find_subscription_address(subscriber.pubkey(), zero_price_plan);
    let subscription_account_2 = find_subscription_address(subscriber_2.pubkey(), zero_price_plan);

    let stranger = funded_keypair(&mut svm);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_subscription::close_subscription_ix_with_provider(
                &stranger.pubkey(),
                subscription_account,
                provider,
                rent_payer.pubkey(),
                None,
            )],
            &[&stranger],
        ),
        DvpnError::Unauthorized,
    );

    let grantee = Keypair::new();
    svm.airdrop(&grantee.pubkey(), 10_000_000_000).unwrap();
    send(
        &mut svm,
        &[ix_grant::open_grant_ix(
            &provider_owner.pubkey(),
            &rent_payer.pubkey(),
            grantee.pubkey(),
            Pubkey::default(),
            PERM_CLOSE_FREE_SUBSCRIPTION,
            0,
            0,
            rent_payer.pubkey(),
        )],
        &[&provider_owner, &rent_payer],
    )
    .unwrap();
    let grant = find_grant(
        &dvpn::ID,
        &provider_owner.pubkey(),
        &grantee.pubkey(),
        &Pubkey::default(),
    )
    .0;
    send(
        &mut svm,
        &[ix_subscription::close_subscription_ix_with_provider(
            &grantee.pubkey(),
            subscription_account,
            provider,
            rent_payer.pubkey(),
            Some(grant),
        )],
        &[&grantee],
    )
    .unwrap();
    assert!(is_account_closed(&svm, &subscription_account));

    let foreign_owner = funded_keypair(&mut svm);
    let foreign_grantee = Keypair::new();
    svm.airdrop(&foreign_grantee.pubkey(), 10_000_000_000)
        .unwrap();
    send(
        &mut svm,
        &[ix_grant::open_grant_ix(
            &foreign_owner.pubkey(),
            &rent_payer.pubkey(),
            foreign_grantee.pubkey(),
            Pubkey::default(),
            PERM_CLOSE_FREE_SUBSCRIPTION,
            0,
            0,
            rent_payer.pubkey(),
        )],
        &[&foreign_owner, &rent_payer],
    )
    .unwrap();
    let foreign_grant = find_grant(
        &dvpn::ID,
        &foreign_owner.pubkey(),
        &foreign_grantee.pubkey(),
        &Pubkey::default(),
    )
    .0;
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_subscription::close_subscription_ix_with_provider(
                &foreign_grantee.pubkey(),
                subscription_account_2,
                provider,
                rent_payer.pubkey(),
                Some(foreign_grant),
            )],
            &[&foreign_grantee],
        ),
        DvpnError::Unauthorized,
    );
}
