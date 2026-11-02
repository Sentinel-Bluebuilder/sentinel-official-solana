use dvpn::error::DvpnError;
use dvpn_core::enums::ListingStatus;
use dvpn_core::permissions::*;
use dvpn_core::seeds::{find_plan, find_subscription};
use dvpn_test_support::*;
use dvpn_test_support::{ix_grant, ix_subscription};
use litesvm::LiteSVM;
use solana_sdk::pubkey::Pubkey;
use solana_sdk::signature::{Keypair, Signer};

fn plant_listed_provider_and_plan(
    svm: &mut LiteSVM,
    cfg: Pubkey,
    provider_owner: &Pubkey,
    price: u64,
) -> (Pubkey, Pubkey) {
    let provider = plant_provider(
        svm,
        cfg,
        &ProviderSpec {
            owner: *provider_owner,
            provider_id: [1u8; 32],
            status: ListingStatus::Listed as u8,
            ..Default::default()
        },
    );
    let plan_id = if price == 0 { [2u8; 32] } else { [3u8; 32] };
    let plan = find_plan(&dvpn::ID, &provider, &plan_id).0;
    plant_account(
        svm,
        plan,
        &dvpn::state::Plan {
            plan_id,
            provider,
            price,
            duration_seconds: 3_600,
            data_cap_gb: 1,
            max_connections: 1,
            status: ListingStatus::Listed as u8,
            rent_recipient: *provider_owner,
            bump: find_plan(&dvpn::ID, &provider, &plan_id).1,
            reserved: [0u8; 32],
        },
    );
    (provider, plan)
}

#[test]
fn grantee_buys_priced_subscription_for_subscriber() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault) = (genesis.svm, genesis.config, genesis.mint, genesis.vault);
    let provider_owner = Keypair::new();
    let (provider, plan) =
        plant_listed_provider_and_plan(&mut svm, cfg, &provider_owner.pubkey(), 100);
    let subscriber = funded_keypair(&mut svm);
    let grantee = funded_keypair(&mut svm);
    let rent_payer = funded_keypair(&mut svm);
    let owner_token = plant_ata(&mut svm, mint, &subscriber.pubkey(), 1_000);
    approve_delegate(&mut svm, &subscriber, owner_token, mint, cfg, 1_000);
    let grant = open_unscoped_grant(
        &mut svm,
        &subscriber,
        grantee.pubkey(),
        PERM_OPEN_SUBSCRIPTION,
        150,
    );

    let instruction = |grant: Option<Pubkey>| {
        ix_subscription::open_subscription_ix_with_price(
            &grantee.pubkey(),
            &rent_payer.pubkey(),
            subscriber.pubkey(),
            provider,
            plan,
            owner_token,
            cfg,
            mint,
            vault,
            100,
            3_600,
            1,
            1,
            grant,
            rent_payer.pubkey(),
        )
    };
    assert_dvpn_error(
        &send_expect_err(&mut svm, &[instruction(None)], &[&grantee, &rent_payer]),
        DvpnError::Unauthorized,
    );
    svm.expire_blockhash();
    send(
        &mut svm,
        &[instruction(Some(grant))],
        &[&grantee, &rent_payer],
    )
    .unwrap();
    let subscription = read_subscription(
        &svm,
        &find_subscription(&dvpn::ID, &subscriber.pubkey(), &plan).0,
    );
    assert_eq!(subscription.owner, subscriber.pubkey());
    assert_eq!(read_grant(&svm, &grant).allowance, 50);
    assert_eq!(read_token_amount(&svm, &owner_token), 900);
    assert_solvent(&svm, 0);
}

#[test]
fn provider_grantee_gifts_free_subscription() {
    let genesis = genesis();
    let (mut svm, cfg) = (genesis.svm, genesis.config);
    let provider_owner = funded_keypair(&mut svm);
    let (provider, plan) =
        plant_listed_provider_and_plan(&mut svm, cfg, &provider_owner.pubkey(), 0);
    let grantee = funded_keypair(&mut svm);
    let rent_payer = funded_keypair(&mut svm);
    let wallet = Pubkey::new_unique();
    let grant = open_unscoped_grant(
        &mut svm,
        &provider_owner,
        grantee.pubkey(),
        PERM_OPEN_SUBSCRIPTION,
        0,
    );
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_subscription::open_subscription_ix_free(
                &grantee.pubkey(),
                &rent_payer.pubkey(),
                wallet,
                provider,
                plan,
                3_600,
                1,
                1,
                Some(grant),
                rent_payer.pubkey(),
            )],
            &[&grantee, &rent_payer],
        ),
        DvpnError::PermissionNotGranted,
    );
    send(
        &mut svm,
        &[ix_grant::update_grant_ix(
            &provider_owner.pubkey(),
            grant,
            Some(PERM_OPEN_FREE_SUBSCRIPTION),
            None,
            None,
        )],
        &[&provider_owner],
    )
    .unwrap();
    svm.expire_blockhash();
    send(
        &mut svm,
        &[ix_subscription::open_subscription_ix_free(
            &grantee.pubkey(),
            &rent_payer.pubkey(),
            wallet,
            provider,
            plan,
            3_600,
            1,
            1,
            Some(grant),
            rent_payer.pubkey(),
        )],
        &[&grantee, &rent_payer],
    )
    .unwrap();
    assert_eq!(
        read_subscription(&svm, &find_subscription(&dvpn::ID, &wallet, &plan).0).price,
        0
    );
}

#[test]
fn subscriber_grantee_closes_and_subscriber_with_grant_is_rejected() {
    let genesis = genesis();
    let (mut svm, cfg) = (genesis.svm, genesis.config);
    set_clock(&mut svm, 1_000);
    let provider_owner = funded_keypair(&mut svm);
    let (provider, plan) =
        plant_listed_provider_and_plan(&mut svm, cfg, &provider_owner.pubkey(), 0);
    let subscriber = funded_keypair(&mut svm);
    let subscriber_grantee = funded_keypair(&mut svm);
    let rent_payer = funded_keypair(&mut svm);
    let open = |svm: &mut LiteSVM| {
        svm.expire_blockhash();
        send(
            svm,
            &[ix_subscription::open_subscription_ix_free(
                &provider_owner.pubkey(),
                &rent_payer.pubkey(),
                subscriber.pubkey(),
                provider,
                plan,
                3_600,
                1,
                1,
                None,
                rent_payer.pubkey(),
            )],
            &[&provider_owner, &rent_payer],
        )
        .unwrap();
        find_subscription(&dvpn::ID, &subscriber.pubkey(), &plan).0
    };
    let close = |signer: &Pubkey, subscription: Pubkey| {
        ix_subscription::close_subscription_ix(
            signer,
            subscription,
            rent_payer.pubkey(),
            Some(find_grant_address(
                subscriber.pubkey(),
                subscriber_grantee.pubkey(),
                Pubkey::default(),
            )),
        )
    };
    open_unscoped_grant(
        &mut svm,
        &subscriber,
        subscriber_grantee.pubkey(),
        PERM_CLOSE_SUBSCRIPTION,
        0,
    );

    let first_subscription = open(&mut svm);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[close(&subscriber.pubkey(), first_subscription)],
            &[&subscriber],
        ),
        DvpnError::Unauthorized,
    );
    send(
        &mut svm,
        &[close(&subscriber_grantee.pubkey(), first_subscription)],
        &[&subscriber_grantee],
    )
    .unwrap();
    assert!(is_account_closed(&svm, &first_subscription));

    let second_subscription = open(&mut svm);
    set_clock(&mut svm, 1_000 + 3_600);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[close(&subscriber.pubkey(), second_subscription)],
            &[&subscriber],
        ),
        DvpnError::Unauthorized,
    );
}
