#![allow(clippy::unwrap_used, clippy::indexing_slicing)]
use dvpn::error::DvpnError;
use dvpn_core::args::NodePrices;
use dvpn_core::constants::ALLOWANCE_UNLIMITED;
use dvpn_core::enums::{ListingStatus, PositionMode, PositionTerms};
use dvpn_core::permissions::*;
use dvpn_core::seeds::{find_grant, find_lease};
use dvpn_core::voucher::Voucher;
use solana_sdk::instruction::InstructionError;
#[allow(deprecated)]
use solana_sdk::transaction::TransactionError;
use solana_sdk::{pubkey::Pubkey, signature::Keypair, signer::Signer};
use spl_token_2022_interface::error::TokenError;

#[allow(clippy::too_many_arguments)]
fn stream_lease_spec(
    provider: &Pubkey,
    owner: &Pubkey,
    lease_id: [u8; 32],
    escrow: u64,
    rate_per_hour: u64,
    stream_started_at: i64,
) -> dvpn_test_support::LeaseSpec {
    dvpn_test_support::LeaseSpec {
        mode: PositionMode::Stream as u8,
        rate_per_hour,
        stream_started_at,
        voucher_signer: Pubkey::default(),
        escrow,
        ..dvpn_test_support::LeaseSpec::voucher(*provider, *owner, lease_id)
    }
}

#[allow(clippy::too_many_arguments)]
fn stream_redeemable_lease_spec(
    provider: &Pubkey,
    owner: &Pubkey,
    lease_id: [u8; 32],
    node: &Pubkey,
    escrow: u64,
    rate_per_hour: u64,
    stream_started_at: i64,
) -> dvpn_test_support::LeaseSpec {
    dvpn_test_support::LeaseSpec {
        node: *node,
        ..stream_lease_spec(
            provider,
            owner,
            lease_id,
            escrow,
            rate_per_hour,
            stream_started_at,
        )
    }
}

#[test]
fn open_lease_voucher_draws_balance_then_revenue_then_wallet() {
    let mut genesis = dvpn_test_support::genesis();
    let now = 1_700_000_000i64;
    dvpn_test_support::set_clock(&mut genesis.svm, now);
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), now),
    );
    let provider_owner = Keypair::new();
    genesis
        .svm
        .airdrop(&provider_owner.pubkey(), 10_000_000_000)
        .unwrap();
    let provider = dvpn_test_support::plant_provider(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ProviderSpec {
            balance: 30,
            revenue: 20,
            ..dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [1u8; 32])
        },
    );
    let owner_token = dvpn_test_support::plant_ata(
        &mut genesis.svm,
        genesis.mint,
        &provider_owner.pubkey(),
        1_000,
    );

    let voucher_signer_key = dvpn_test_support::voucher_signing_key(3);
    let voucher_signer = Pubkey::new_from_array(voucher_signer_key.verifying_key().to_bytes());
    let instruction = dvpn_test_support::ix_positions::open_lease_ix(
        &genesis,
        &provider,
        &node,
        &owner_token,
        [2u8; 32],
        100,
        PositionTerms::Voucher { voucher_signer },
        &provider_owner.pubkey(),
        &provider_owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&provider_owner]);

    let lease_state = dvpn_test_support::read_lease(
        &genesis.svm,
        &find_lease(&dvpn::ID, &provider, &[2u8; 32]).0,
    );
    assert_eq!(lease_state.escrow, 100);
    assert_eq!(lease_state.owner, provider_owner.pubkey());
    assert_eq!(lease_state.opener, provider_owner.pubkey());
    assert_eq!(lease_state.voucher_signer, voucher_signer);
    let provider_state = dvpn_test_support::read_provider(&genesis.svm, &provider);
    assert_eq!(
        (
            provider_state.balance,
            provider_state.revenue,
            provider_state.open_leases
        ),
        (0, 0, 1)
    );
    assert_eq!(
        dvpn_test_support::read_token_amount(&genesis.svm, &genesis.vault),
        100
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn open_lease_delisted_provider_fails_provider_not_listed() {
    let mut genesis = dvpn_test_support::genesis();
    let now = 1_700_000_000i64;
    dvpn_test_support::set_clock(&mut genesis.svm, now);
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), now),
    );
    let provider_owner = Keypair::new();
    genesis
        .svm
        .airdrop(&provider_owner.pubkey(), 10_000_000_000)
        .unwrap();
    let provider = dvpn_test_support::plant_provider(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ProviderSpec {
            status: ListingStatus::Delisted as u8,
            ..dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [18u8; 32])
        },
    );
    let owner_token =
        dvpn_test_support::plant_ata(&mut genesis.svm, genesis.mint, &provider_owner.pubkey(), 0);

    let instruction = dvpn_test_support::ix_positions::open_lease_ix(
        &genesis,
        &provider,
        &node,
        &owner_token,
        [19u8; 32],
        0,
        PositionTerms::Voucher {
            voucher_signer: provider_owner.pubkey(),
        },
        &provider_owner.pubkey(),
        &provider_owner.pubkey(),
    );
    let failed =
        dvpn_test_support::send_expect_err(&mut genesis.svm, &[instruction], &[&provider_owner]);
    dvpn_test_support::assert_dvpn_error(&failed, DvpnError::ProviderNotListed);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn open_lease_fails_when_all_paused() {
    let mut genesis = dvpn_test_support::genesis_paused();
    let now = 1_700_000_000i64;
    dvpn_test_support::set_clock(&mut genesis.svm, now);
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), now),
    );
    let provider_owner = Keypair::new();
    genesis
        .svm
        .airdrop(&provider_owner.pubkey(), 10_000_000_000)
        .unwrap();
    let provider = dvpn_test_support::plant_provider(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [23u8; 32]),
    );
    let owner_token =
        dvpn_test_support::plant_ata(&mut genesis.svm, genesis.mint, &provider_owner.pubkey(), 0);

    let instruction = dvpn_test_support::ix_positions::open_lease_ix(
        &genesis,
        &provider,
        &node,
        &owner_token,
        [24u8; 32],
        0,
        PositionTerms::Voucher {
            voucher_signer: provider_owner.pubkey(),
        },
        &provider_owner.pubkey(),
        &provider_owner.pubkey(),
    );
    let failed =
        dvpn_test_support::send_expect_err(&mut genesis.svm, &[instruction], &[&provider_owner]);
    dvpn_test_support::assert_dvpn_error(&failed, DvpnError::OperationPaused);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn grantee_open_at_exactly_allowance_leaves_zero_one_more_fails() {
    let mut genesis = dvpn_test_support::genesis();
    let now = 1_700_000_000i64;
    dvpn_test_support::set_clock(&mut genesis.svm, now);
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), now),
    );
    let provider_owner = Keypair::new();
    genesis
        .svm
        .airdrop(&provider_owner.pubkey(), 10_000_000_000)
        .unwrap();
    let provider = dvpn_test_support::plant_provider(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [200u8; 32]),
    );
    let grantee = Keypair::new();
    genesis
        .svm
        .airdrop(&grantee.pubkey(), 10_000_000_000)
        .unwrap();
    let grant = dvpn_test_support::plant_grant(
        &mut genesis.svm,
        &dvpn_test_support::GrantSpec {
            granter: provider_owner.pubkey(),
            grantee: grantee.pubkey(),
            target: Pubkey::default(),
            permissions: PERM_OPEN_LEASE
                | PERM_FUND_LEASES
                | PERM_START_CLOSE_LEASE
                | PERM_CLOSE_LEASE,
            expires_at: 0,
            allowance: 100,
        },
    );
    let owner_token = dvpn_test_support::plant_ata(
        &mut genesis.svm,
        genesis.mint,
        &provider_owner.pubkey(),
        1_000_000,
    );
    dvpn_test_support::approve_delegate(
        &mut genesis.svm,
        &provider_owner,
        owner_token,
        genesis.mint,
        genesis.config,
        1_000_000,
    );

    let instruction = dvpn_test_support::ix_positions::open_lease_ix_as_grantee(
        &genesis,
        &provider,
        &grantee.pubkey(),
        &grant,
        &node,
        &owner_token,
        [201u8; 32],
        100,
        PositionTerms::Voucher {
            voucher_signer: grantee.pubkey(),
        },
        &grantee.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&grantee]);
    assert_eq!(
        dvpn_test_support::read_grant(&genesis.svm, &grant).allowance,
        0
    );

    genesis.svm.expire_blockhash();
    let second_instruction = dvpn_test_support::ix_positions::open_lease_ix_as_grantee(
        &genesis,
        &provider,
        &grantee.pubkey(),
        &grant,
        &node,
        &owner_token,
        [202u8; 32],
        1,
        PositionTerms::Voucher {
            voucher_signer: grantee.pubkey(),
        },
        &grantee.pubkey(),
    );
    let failed =
        dvpn_test_support::send_expect_err(&mut genesis.svm, &[second_instruction], &[&grantee]);
    dvpn_test_support::assert_dvpn_error(&failed, DvpnError::AmountExceedsAllowance);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn grantee_open_wallet_shortfall_no_approval_fails_token_error() {
    let mut genesis = dvpn_test_support::genesis();
    let now = 1_700_000_000i64;
    dvpn_test_support::set_clock(&mut genesis.svm, now);
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), now),
    );
    let provider_owner = Keypair::new();
    genesis
        .svm
        .airdrop(&provider_owner.pubkey(), 10_000_000_000)
        .unwrap();
    let provider = dvpn_test_support::plant_provider(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [209u8; 32]),
    );
    let grantee = Keypair::new();
    genesis
        .svm
        .airdrop(&grantee.pubkey(), 10_000_000_000)
        .unwrap();
    let grant = dvpn_test_support::plant_grant(
        &mut genesis.svm,
        &dvpn_test_support::GrantSpec {
            granter: provider_owner.pubkey(),
            grantee: grantee.pubkey(),
            target: Pubkey::default(),
            permissions: PERM_OPEN_LEASE
                | PERM_FUND_LEASES
                | PERM_START_CLOSE_LEASE
                | PERM_CLOSE_LEASE,
            expires_at: 0,
            allowance: 1_000,
        },
    );

    let owner_token = dvpn_test_support::plant_ata(
        &mut genesis.svm,
        genesis.mint,
        &provider_owner.pubkey(),
        1_000,
    );

    let instruction = dvpn_test_support::ix_positions::open_lease_ix_as_grantee(
        &genesis,
        &provider,
        &grantee.pubkey(),
        &grant,
        &node,
        &owner_token,
        [210u8; 32],
        100,
        PositionTerms::Voucher {
            voucher_signer: grantee.pubkey(),
        },
        &grantee.pubkey(),
    );
    let failed = dvpn_test_support::send_expect_err(&mut genesis.svm, &[instruction], &[&grantee]);
    match &failed.err {
        TransactionError::InstructionError(_, InstructionError::Custom(code)) => {
            assert_eq!(*code, TokenError::OwnerMismatch as u32);
        }
        other => panic!(
            "expected Token-2022 OwnerMismatch, got {other:?}\n{}",
            failed.meta.pretty_logs()
        ),
    }
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn fund_leases_across_balance_revenue_wallet_one_cpi() {
    let mut genesis = dvpn_test_support::genesis();
    let provider_owner = Keypair::new();
    genesis
        .svm
        .airdrop(&provider_owner.pubkey(), 10_000_000_000)
        .unwrap();
    let spec = dvpn_test_support::ProviderSpec {
        balance: 40,
        revenue: 40,
        ..dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [38u8; 32])
    };
    let provider = dvpn_test_support::plant_provider(&mut genesis.svm, genesis.config, &spec);
    let owner_token = dvpn_test_support::plant_ata(
        &mut genesis.svm,
        genesis.mint,
        &provider_owner.pubkey(),
        1_000,
    );
    let mut leases: Vec<Pubkey> = (0u8..3)
        .map(|seed| {
            dvpn_test_support::plant_lease(
                &mut genesis.svm,
                genesis.config,
                &dvpn_test_support::LeaseSpec::voucher(
                    provider,
                    provider_owner.pubkey(),
                    [seed; 32],
                ),
            )
        })
        .collect();
    leases.sort();

    let instruction = dvpn_test_support::ix_positions::fund_leases_ix(
        &genesis,
        &provider,
        &owner_token,
        &leases,
        vec![50, 50, 50],
    );
    dvpn_test_support::send(&mut genesis.svm, &[instruction], &[&provider_owner]).unwrap();

    let provider_state = dvpn_test_support::read_provider(&genesis.svm, &provider);
    assert_eq!((provider_state.balance, provider_state.revenue), (0, 0));
    assert_eq!(
        dvpn_test_support::read_token_amount(&genesis.svm, &genesis.vault),
        150
    );
    for lease_address in &leases {
        assert_eq!(
            dvpn_test_support::read_lease(&genesis.svm, lease_address).escrow,
            50
        );
    }
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn fund_leases_second_grantee_of_same_owner_succeeds_with_fund_bit() {
    let mut genesis = dvpn_test_support::genesis();
    let provider_owner = Keypair::new();
    genesis
        .svm
        .airdrop(&provider_owner.pubkey(), 10_000_000_000)
        .unwrap();
    let grantee2 = Keypair::new();
    genesis
        .svm
        .airdrop(&grantee2.pubkey(), 10_000_000_000)
        .unwrap();
    let provider = dvpn_test_support::plant_provider(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [48u8; 32]),
    );
    let grant2 = dvpn_test_support::plant_grant(
        &mut genesis.svm,
        &dvpn_test_support::GrantSpec {
            granter: provider_owner.pubkey(),
            grantee: grantee2.pubkey(),
            target: Pubkey::default(),
            permissions: PERM_FUND_LEASES,
            expires_at: 0,
            allowance: 1_000_000,
        },
    );
    let owner_token = dvpn_test_support::plant_ata(
        &mut genesis.svm,
        genesis.mint,
        &provider_owner.pubkey(),
        1_000,
    );
    dvpn_test_support::approve_delegate(
        &mut genesis.svm,
        &provider_owner,
        owner_token,
        genesis.mint,
        genesis.config,
        1_000,
    );
    let lease = dvpn_test_support::plant_lease(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::LeaseSpec::voucher(provider, provider_owner.pubkey(), [49u8; 32]),
    );

    let instruction = dvpn_test_support::ix_positions::fund_leases_ix_as_grantee(
        &genesis,
        &provider,
        &grantee2.pubkey(),
        &grant2,
        &owner_token,
        &[lease],
        vec![10],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&grantee2]);
    assert_eq!(
        dvpn_test_support::read_lease(&genesis.svm, &lease).escrow,
        dvpn_test_support::LeaseSpec::voucher(provider, provider_owner.pubkey(), [49u8; 32]).escrow
            + 10
    );
    assert_eq!(
        dvpn_test_support::read_grant(&genesis.svm, &grant2).allowance,
        1_000_000 - 10
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn redeem_lease_stream_pays_accrued_net_of_fee() {
    let mut genesis = dvpn_test_support::genesis();
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0),
    );
    let provider_owner = Keypair::new();
    let provider = dvpn_test_support::plant_provider(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [107u8; 32]),
    );
    let lease = dvpn_test_support::plant_lease(
        &mut genesis.svm,
        genesis.config,
        &stream_redeemable_lease_spec(
            &provider,
            &provider_owner.pubkey(),
            [108u8; 32],
            &node,
            100_000_000,
            10_000_000,
            1_000,
        ),
    );
    dvpn_test_support::set_clock(&mut genesis.svm, 1_090);
    let redeemer = Keypair::new();
    genesis
        .svm
        .airdrop(&redeemer.pubkey(), 10_000_000_000)
        .unwrap();

    let instruction = dvpn_test_support::ix_positions::redeem_lease_streams_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[lease],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&redeemer]);

    assert_eq!(
        dvpn_test_support::read_node(&genesis.svm, &node).revenue,
        250_000
    );
    assert_eq!(
        dvpn_test_support::read_lease(&genesis.svm, &lease).redeemed,
        250_000
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn redeem_lease_stream_caps_at_escrow() {
    let mut genesis = dvpn_test_support::genesis();
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0),
    );
    let provider_owner = Keypair::new();
    let provider = dvpn_test_support::plant_provider(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [109u8; 32]),
    );
    let lease = dvpn_test_support::plant_lease(
        &mut genesis.svm,
        genesis.config,
        &stream_redeemable_lease_spec(
            &provider,
            &provider_owner.pubkey(),
            [110u8; 32],
            &node,
            100,
            10,
            0,
        ),
    );
    let redeemer = Keypair::new();
    genesis
        .svm
        .airdrop(&redeemer.pubkey(), 10_000_000_000)
        .unwrap();

    dvpn_test_support::set_clock(&mut genesis.svm, 5 * 3_600);
    let instruction = dvpn_test_support::ix_positions::redeem_lease_streams_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[lease],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&redeemer]);
    assert_eq!(
        dvpn_test_support::read_lease(&genesis.svm, &lease).redeemed,
        50
    );

    genesis.svm.expire_blockhash();
    dvpn_test_support::set_clock(&mut genesis.svm, 7 * 3_600);
    let instruction = dvpn_test_support::ix_positions::redeem_lease_streams_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[lease],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&redeemer]);
    assert_eq!(
        dvpn_test_support::read_lease(&genesis.svm, &lease).redeemed,
        70
    );

    genesis.svm.expire_blockhash();
    dvpn_test_support::set_clock(&mut genesis.svm, 12 * 3_600);
    let instruction = dvpn_test_support::ix_positions::redeem_lease_streams_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[lease],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&redeemer]);
    assert_eq!(
        dvpn_test_support::read_lease(&genesis.svm, &lease).redeemed,
        100
    );

    genesis.svm.expire_blockhash();
    dvpn_test_support::set_clock(&mut genesis.svm, 15 * 3_600);
    let instruction = dvpn_test_support::ix_positions::redeem_lease_streams_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[lease],
    );
    let failed = dvpn_test_support::send_expect_err(&mut genesis.svm, &[instruction], &[&redeemer]);
    dvpn_test_support::assert_dvpn_error(&failed, DvpnError::RedeemableZero);

    assert_eq!(
        dvpn_test_support::read_node(&genesis.svm, &node).revenue,
        100
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn redeem_lease_voucher_by_grantee_pays_net_of_fee() {
    let mut genesis = dvpn_test_support::genesis();
    {
        let mut config_state = dvpn_test_support::read_config(&genesis.svm);
        config_state.fee_bps = 200;
        dvpn_test_support::plant_account(&mut genesis.svm, genesis.config, &config_state);
    }
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0),
    );
    let provider_owner = Keypair::new();
    let provider = dvpn_test_support::plant_provider(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [60u8; 32]),
    );
    let voucher_signer_key = dvpn_test_support::voucher_signing_key(20);
    let voucher_signer = Pubkey::new_from_array(voucher_signer_key.verifying_key().to_bytes());
    let lease = dvpn_test_support::plant_lease(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::LeaseSpec {
            node,
            escrow: 1_000_000,
            voucher_signer,
            ..dvpn_test_support::LeaseSpec::voucher(provider, provider_owner.pubkey(), [61u8; 32])
        },
    );
    let sig = dvpn_test_support::sign_lease_voucher(&voucher_signer_key, &lease, 0, 500_000);
    let vouchers = vec![Voucher {
        cumulative_amount: 500_000,
        signature: sig,
    }];
    let redeemer = Keypair::new();
    genesis
        .svm
        .airdrop(&redeemer.pubkey(), 10_000_000_000)
        .unwrap();
    let instruction = dvpn_test_support::ix_positions::redeem_lease_vouchers_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[lease],
        vouchers,
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&redeemer]);
    let node_state = dvpn_test_support::read_node(&genesis.svm, &node);
    assert_eq!(
        (node_state.revenue, node_state.fees_accrued),
        (490_000, 10_000)
    );
    assert_eq!(
        dvpn_test_support::read_lease(&genesis.svm, &lease).redeemed,
        500_000
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn redeem_lease_voucher_signed_for_different_lease_fails_signature_invalid() {
    let mut genesis = dvpn_test_support::genesis();
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0),
    );
    let provider_owner = Keypair::new();
    let provider = dvpn_test_support::plant_provider(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [66u8; 32]),
    );
    let voucher_signer_key = dvpn_test_support::voucher_signing_key(23);
    let voucher_signer = Pubkey::new_from_array(voucher_signer_key.verifying_key().to_bytes());
    let lease_a = dvpn_test_support::plant_lease(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::LeaseSpec {
            node,
            escrow: 1_000_000,
            voucher_signer,
            ..dvpn_test_support::LeaseSpec::voucher(provider, provider_owner.pubkey(), [67u8; 32])
        },
    );
    let lease_b = dvpn_test_support::plant_lease(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::LeaseSpec {
            node,
            escrow: 1_000_000,
            voucher_signer,
            ..dvpn_test_support::LeaseSpec::voucher(provider, provider_owner.pubkey(), [68u8; 32])
        },
    );

    let sig = dvpn_test_support::sign_lease_voucher(&voucher_signer_key, &lease_b, 0, 100_000);
    let vouchers = vec![Voucher {
        cumulative_amount: 100_000,
        signature: sig,
    }];
    let redeemer = Keypair::new();
    genesis
        .svm
        .airdrop(&redeemer.pubkey(), 10_000_000_000)
        .unwrap();
    let instruction = dvpn_test_support::ix_positions::redeem_lease_vouchers_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[lease_a],
        vouchers,
    );
    let failed = dvpn_test_support::send_expect_err(&mut genesis.svm, &[instruction], &[&redeemer]);
    dvpn_test_support::assert_dvpn_error(&failed, DvpnError::InvalidVoucherSignature);
    assert_eq!(
        dvpn_test_support::read_lease(&genesis.svm, &lease_a).redeemed,
        0
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn redeem_lease_voucher_double_redeem_second_call_fails_nothing_to_redeem() {
    let mut genesis = dvpn_test_support::genesis();
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0),
    );
    let provider_owner = Keypair::new();
    let provider = dvpn_test_support::plant_provider(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [72u8; 32]),
    );
    let voucher_signer_key = dvpn_test_support::voucher_signing_key(26);
    let voucher_signer = Pubkey::new_from_array(voucher_signer_key.verifying_key().to_bytes());
    let lease = dvpn_test_support::plant_lease(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::LeaseSpec {
            node,
            escrow: 1_000_000,
            voucher_signer,
            ..dvpn_test_support::LeaseSpec::voucher(provider, provider_owner.pubkey(), [73u8; 32])
        },
    );
    let sig = dvpn_test_support::sign_lease_voucher(&voucher_signer_key, &lease, 0, 500_000);
    let redeemer = Keypair::new();
    genesis
        .svm
        .airdrop(&redeemer.pubkey(), 10_000_000_000)
        .unwrap();

    let instruction = dvpn_test_support::ix_positions::redeem_lease_vouchers_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[lease],
        vec![Voucher {
            cumulative_amount: 500_000,
            signature: sig,
        }],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&redeemer]);
    assert_eq!(
        dvpn_test_support::read_lease(&genesis.svm, &lease).redeemed,
        500_000
    );
    let n_before = dvpn_test_support::read_node(&genesis.svm, &node);

    genesis.svm.expire_blockhash();
    let second_instruction = dvpn_test_support::ix_positions::redeem_lease_vouchers_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[lease],
        vec![Voucher {
            cumulative_amount: 500_000,
            signature: sig,
        }],
    );
    let failed =
        dvpn_test_support::send_expect_err(&mut genesis.svm, &[second_instruction], &[&redeemer]);
    dvpn_test_support::assert_dvpn_error(&failed, DvpnError::RedeemableZero);
    assert_eq!(
        dvpn_test_support::read_lease(&genesis.svm, &lease).redeemed,
        500_000
    );
    let n_after = dvpn_test_support::read_node(&genesis.svm, &node);
    assert_eq!(
        (n_before.revenue, n_before.fees_accrued),
        (n_after.revenue, n_after.fees_accrued)
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn redeem_lease_voucher_fee_exactness_large_values_u128_safe() {
    let mut genesis = dvpn_test_support::genesis();
    {
        let mut config_state = dvpn_test_support::read_config(&genesis.svm);
        config_state.fee_bps = 9_999;
        dvpn_test_support::plant_account(&mut genesis.svm, genesis.config, &config_state);
    }
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0),
    );
    let provider_owner = Keypair::new();
    let provider = dvpn_test_support::plant_provider(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [100u8; 32]),
    );
    let voucher_signer_key = dvpn_test_support::voucher_signing_key(36);
    let voucher_signer = Pubkey::new_from_array(voucher_signer_key.verifying_key().to_bytes());
    let cumulative_amount = u64::MAX;
    let lease = dvpn_test_support::plant_lease(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::LeaseSpec {
            node,
            escrow: u64::MAX,
            voucher_signer,
            ..dvpn_test_support::LeaseSpec::voucher(provider, provider_owner.pubkey(), [101u8; 32])
        },
    );
    let sig =
        dvpn_test_support::sign_lease_voucher(&voucher_signer_key, &lease, 0, cumulative_amount);
    let vouchers = vec![Voucher {
        cumulative_amount,
        signature: sig,
    }];
    let redeemer = Keypair::new();
    genesis
        .svm
        .airdrop(&redeemer.pubkey(), 10_000_000_000)
        .unwrap();
    let instruction = dvpn_test_support::ix_positions::redeem_lease_vouchers_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[lease],
        vouchers,
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&redeemer]);
    let node_state = dvpn_test_support::read_node(&genesis.svm, &node);
    let expected_fee = u64::try_from(u128::from(cumulative_amount) * 9_999 / 10_000).unwrap();
    assert_eq!(node_state.fees_accrued, expected_fee);
    assert_eq!(node_state.revenue, cumulative_amount - expected_fee);
    assert_eq!(
        u128::from(node_state.revenue) + u128::from(node_state.fees_accrued),
        u128::from(cumulative_amount)
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn close_lease_before_grace_fails_grace_not_elapsed() {
    let mut genesis = dvpn_test_support::genesis();
    {
        let mut config_state = dvpn_test_support::read_config(&genesis.svm);
        config_state.lease_close_grace_seconds = 300;
        dvpn_test_support::plant_account(&mut genesis.svm, genesis.config, &config_state);
    }
    let provider_owner = Keypair::new();
    genesis
        .svm
        .airdrop(&provider_owner.pubkey(), 10_000_000_000)
        .unwrap();
    let provider = dvpn_test_support::plant_provider(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ProviderSpec {
            open_leases: 2,
            ..dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [127u8; 32])
        },
    );

    let fully_redeemed_lease = dvpn_test_support::plant_lease(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::LeaseSpec {
            escrow: 50,
            redeemed: 50,
            rent_recipient: provider_owner.pubkey(),
            ..dvpn_test_support::LeaseSpec::voucher(provider, provider_owner.pubkey(), [128u8; 32])
        },
    );

    let empty_lease = dvpn_test_support::plant_lease(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::LeaseSpec {
            escrow: 0,
            redeemed: 0,
            rent_recipient: provider_owner.pubkey(),
            ..dvpn_test_support::LeaseSpec::voucher(provider, provider_owner.pubkey(), [129u8; 32])
        },
    );
    dvpn_test_support::set_clock(&mut genesis.svm, 10_000);
    let start_full = dvpn_test_support::ix_positions::start_close_lease_ix(
        &genesis,
        &provider,
        &fully_redeemed_lease,
        &provider_owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[start_full], &[&provider_owner]);
    genesis.svm.expire_blockhash();
    let start_zero = dvpn_test_support::ix_positions::start_close_lease_ix(
        &genesis,
        &provider,
        &empty_lease,
        &provider_owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[start_zero], &[&provider_owner]);
    dvpn_test_support::set_clock(&mut genesis.svm, 10_299);
    let signer = Keypair::new();
    genesis
        .svm
        .airdrop(&signer.pubkey(), 10_000_000_000)
        .unwrap();

    let ix_full = dvpn_test_support::ix_positions::close_lease_ix(
        &genesis,
        &provider,
        &fully_redeemed_lease,
        &signer.pubkey(),
        None,
        &provider_owner.pubkey(),
    );
    let failed = dvpn_test_support::send_expect_err(&mut genesis.svm, &[ix_full], &[&signer]);
    dvpn_test_support::assert_dvpn_error(&failed, DvpnError::GraceNotElapsed);

    genesis.svm.expire_blockhash();
    let ix_zero = dvpn_test_support::ix_positions::close_lease_ix(
        &genesis,
        &provider,
        &empty_lease,
        &signer.pubkey(),
        None,
        &provider_owner.pubkey(),
    );
    let failed = dvpn_test_support::send_expect_err(&mut genesis.svm, &[ix_zero], &[&signer]);
    dvpn_test_support::assert_dvpn_error(&failed, DvpnError::GraceNotElapsed);

    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn start_close_lease_by_stranger_fails_unauthorized() {
    let mut genesis = dvpn_test_support::genesis();
    let provider_owner = Keypair::new();
    let provider = dvpn_test_support::plant_provider(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ProviderSpec {
            open_leases: 1,
            ..dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [130u8; 32])
        },
    );
    let stranger = Keypair::new();
    let lease_address = dvpn_test_support::plant_lease(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::LeaseSpec {
            escrow: 100,
            redeemed: 0,
            rent_recipient: provider_owner.pubkey(),
            ..dvpn_test_support::LeaseSpec::voucher(provider, provider_owner.pubkey(), [131u8; 32])
        },
    );
    genesis
        .svm
        .airdrop(&stranger.pubkey(), 10_000_000_000)
        .unwrap();

    let instruction = dvpn_test_support::ix_positions::start_close_lease_ix(
        &genesis,
        &provider,
        &lease_address,
        &stranger.pubkey(),
    );
    let failed = dvpn_test_support::send_expect_err(&mut genesis.svm, &[instruction], &[&stranger]);
    dvpn_test_support::assert_dvpn_error(&failed, DvpnError::Unauthorized);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn close_lease_third_party_after_window_credits_provider_balance_and_refunds_rent_recipient() {
    let mut genesis = dvpn_test_support::genesis();
    {
        let mut config_state = dvpn_test_support::read_config(&genesis.svm);
        config_state.lease_close_grace_seconds = 300;
        dvpn_test_support::plant_account(&mut genesis.svm, genesis.config, &config_state);
    }
    let provider_owner = Keypair::new();
    genesis
        .svm
        .airdrop(&provider_owner.pubkey(), 10_000_000_000)
        .unwrap();
    let provider = dvpn_test_support::plant_provider(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ProviderSpec {
            open_leases: 1,
            ..dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [142u8; 32])
        },
    );
    let rent_payer = Keypair::new();
    let lease_address = dvpn_test_support::plant_lease(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::LeaseSpec {
            escrow: 100,
            redeemed: 80,
            rent_recipient: rent_payer.pubkey(),
            ..dvpn_test_support::LeaseSpec::voucher(provider, provider_owner.pubkey(), [143u8; 32])
        },
    );
    dvpn_test_support::set_clock(&mut genesis.svm, 10_000);
    let start_ix = dvpn_test_support::ix_positions::start_close_lease_ix(
        &genesis,
        &provider,
        &lease_address,
        &provider_owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[start_ix], &[&provider_owner]);
    dvpn_test_support::set_clock(&mut genesis.svm, 10_300);
    let stranger = Keypair::new();
    genesis
        .svm
        .airdrop(&stranger.pubkey(), 10_000_000_000)
        .unwrap();
    let close_ix = dvpn_test_support::ix_positions::close_lease_ix(
        &genesis,
        &provider,
        &lease_address,
        &stranger.pubkey(),
        None,
        &rent_payer.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[close_ix], &[&stranger]);
    let provider_state = dvpn_test_support::read_provider(&genesis.svm, &provider);
    assert_eq!(
        (provider_state.balance, provider_state.open_leases),
        (20, 0)
    );
    assert!(dvpn_test_support::is_account_closed(
        &genesis.svm,
        &lease_address
    ));
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn grantee_opened_stream_leases_close_by_owner_and_grantee_then_grant_closable() {
    let mut genesis = dvpn_test_support::genesis();
    let now = 1_700_000_000i64;
    dvpn_test_support::set_clock(&mut genesis.svm, now);
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec {
            prices: NodePrices {
                lease_price_per_hour: 10,
                ..Default::default()
            },
            ..dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), now)
        },
    );
    let provider_owner = Keypair::new();
    genesis
        .svm
        .airdrop(&provider_owner.pubkey(), 10_000_000_000)
        .unwrap();
    let provider = dvpn_test_support::plant_provider(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [220u8; 32]),
    );
    let rent_payer = Keypair::new();
    genesis
        .svm
        .airdrop(&rent_payer.pubkey(), 10_000_000_000)
        .unwrap();
    let grantee = Keypair::new();
    genesis
        .svm
        .airdrop(&grantee.pubkey(), 10_000_000_000)
        .unwrap();
    let open_grant = dvpn_test_support::ix_grant::open_grant_ix(
        &provider_owner.pubkey(),
        &rent_payer.pubkey(),
        grantee.pubkey(),
        Pubkey::default(),
        PERM_OPEN_LEASE | PERM_CLOSE_LEASE,
        0,
        ALLOWANCE_UNLIMITED,
        rent_payer.pubkey(),
    );
    dvpn_test_support::send_expect_ok(
        &mut genesis.svm,
        &[open_grant],
        &[&provider_owner, &rent_payer],
    );
    let grant = find_grant(
        &dvpn::ID,
        &provider_owner.pubkey(),
        &grantee.pubkey(),
        &Pubkey::default(),
    )
    .0;
    let owner_token =
        dvpn_test_support::plant_ata(&mut genesis.svm, genesis.mint, &provider_owner.pubkey(), 0);

    let mut leases = Vec::new();
    for lease_id in [[221u8; 32], [222u8; 32]] {
        genesis.svm.expire_blockhash();
        let open = dvpn_test_support::ix_positions::open_lease_ix_as_grantee(
            &genesis,
            &provider,
            &grantee.pubkey(),
            &grant,
            &node,
            &owner_token,
            lease_id,
            0,
            PositionTerms::Stream {
                expected_rate_per_hour: 10,
            },
            &grantee.pubkey(),
        );
        dvpn_test_support::send_expect_ok(&mut genesis.svm, &[open], &[&grantee]);
        leases.push(find_lease(&dvpn::ID, &provider, &lease_id).0);
        assert_eq!(
            dvpn_test_support::read_lease(&genesis.svm, leases.last().unwrap()).voucher_signer,
            Pubkey::default()
        );
    }

    genesis.svm.expire_blockhash();
    let close_by_owner = dvpn_test_support::ix_positions::close_lease_ix(
        &genesis,
        &provider,
        &leases[0],
        &provider_owner.pubkey(),
        Some(node),
        &grantee.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[close_by_owner], &[&provider_owner]);

    genesis.svm.expire_blockhash();
    let close_by_grantee = dvpn_test_support::ix_positions::close_lease_ix_by(
        &grantee.pubkey(),
        Some(grant),
        &genesis,
        &provider,
        &leases[1],
        Some(node),
        &grantee.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[close_by_grantee], &[&grantee]);
    assert_eq!(
        dvpn_test_support::read_provider(&genesis.svm, &provider).open_leases,
        0
    );

    genesis.svm.expire_blockhash();
    let close_grant = dvpn_test_support::ix_grant::close_grant_ix(
        &provider_owner.pubkey(),
        grant,
        rent_payer.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[close_grant], &[&provider_owner]);
    assert!(dvpn_test_support::read_grant_if_exists(&genesis.svm, &grant).is_none());
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn grantee_names_the_voucher_signer_and_the_node_redeems() {
    let mut genesis = dvpn_test_support::genesis();
    let now = 1_700_000_000i64;
    dvpn_test_support::set_clock(&mut genesis.svm, now);
    let node_owner = dvpn_test_support::funded_keypair(&mut genesis.svm);
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), now),
    );
    let provider_owner = dvpn_test_support::funded_keypair(&mut genesis.svm);
    let provider = dvpn_test_support::plant_provider(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [70u8; 32]),
    );
    let grantee = dvpn_test_support::funded_keypair(&mut genesis.svm);
    let rent_payer = dvpn_test_support::funded_keypair(&mut genesis.svm);
    let voucher_signer_key = dvpn_test_support::voucher_signing_key(71);
    let voucher_signer = Pubkey::new_from_array(voucher_signer_key.verifying_key().to_bytes());
    let owner_token = dvpn_test_support::plant_ata(
        &mut genesis.svm,
        genesis.mint,
        &provider_owner.pubkey(),
        1_000,
    );
    dvpn_test_support::approve_delegate(
        &mut genesis.svm,
        &provider_owner,
        owner_token,
        genesis.mint,
        genesis.config,
        1_000,
    );
    let grant = dvpn_test_support::open_unscoped_grant(
        &mut genesis.svm,
        &provider_owner,
        grantee.pubkey(),
        PERM_OPEN_LEASE,
        1_000,
    );

    let open = dvpn_test_support::ix_positions::open_lease_ix_as_grantee_with_accounts(
        genesis.config,
        genesis.vault,
        genesis.mint,
        &provider,
        &grantee.pubkey(),
        &grant,
        &node,
        &owner_token,
        [71u8; 32],
        1_000,
        PositionTerms::Voucher { voucher_signer },
        &rent_payer.pubkey(),
        &grantee.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[open], &[&grantee, &rent_payer]);
    let lease = find_lease(&dvpn::ID, &provider, &[71u8; 32]).0;
    let state = dvpn_test_support::read_lease(&genesis.svm, &lease);
    assert_eq!(state.opener, grantee.pubkey());
    assert_eq!(state.voucher_signer, voucher_signer);
    assert_eq!(state.rent_recipient, grantee.pubkey());

    let sig =
        dvpn_test_support::sign_lease_voucher(&voucher_signer_key, &lease, state.opened_at, 400);
    let redeem = dvpn_test_support::ix_positions::redeem_lease_vouchers_ix(
        &genesis,
        &node_owner.pubkey(),
        &node,
        &[lease],
        vec![Voucher {
            cumulative_amount: 400,
            signature: sig,
        }],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[redeem], &[&node_owner]);
    assert_eq!(
        dvpn_test_support::read_lease(&genesis.svm, &lease).redeemed,
        400
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}
