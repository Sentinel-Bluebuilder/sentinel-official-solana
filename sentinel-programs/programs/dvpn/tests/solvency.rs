#![allow(
    clippy::unwrap_used,
    clippy::indexing_slicing,
    clippy::too_many_arguments
)]
use dvpn::error::DvpnError;
use dvpn_core::args::NodePrices;
use dvpn_core::enums::{PositionMode, PositionTerms};
use dvpn_core::seeds::{find_channel, find_lease};
use dvpn_core::voucher::Voucher;
use solana_sdk::{pubkey::Pubkey, signature::Keypair, signer::Signer};

fn stream_channel_spec(
    owner: &Pubkey,
    channel_id: [u8; 32],
    node: &Pubkey,
    escrow: u64,
    rate_per_hour: u64,
    stream_started_at: i64,
) -> dvpn_test_support::ChannelSpec {
    dvpn_test_support::ChannelSpec {
        mode: PositionMode::Stream as u8,
        rate_per_hour,
        stream_started_at,
        voucher_signer: Pubkey::default(),
        ..dvpn_test_support::ChannelSpec::voucher_on_node(*owner, channel_id, *node, escrow)
    }
}

fn stream_lease_spec(
    provider: &Pubkey,
    owner: &Pubkey,
    lease_id: [u8; 32],
    node: &Pubkey,
    escrow: u64,
    rate_per_hour: u64,
    stream_started_at: i64,
) -> dvpn_test_support::LeaseSpec {
    dvpn_test_support::LeaseSpec {
        mode: PositionMode::Stream as u8,
        rate_per_hour,
        stream_started_at,
        voucher_signer: Pubkey::default(),
        ..dvpn_test_support::LeaseSpec::voucher_on_node(*provider, *owner, lease_id, *node, escrow)
    }
}

#[test]
fn mixed_mode_lifecycle_conserves_vault() {
    let mut genesis = dvpn_test_support::genesis();
    {
        let mut config_state = dvpn_test_support::read_config(&genesis.svm);
        config_state.fee_bps = 200;
        config_state.channel_close_grace_seconds = 300;
        config_state.lease_close_grace_seconds = 300;
        config_state.node_listing_ttl_seconds = 20_000;
        dvpn_test_support::plant_account(&mut genesis.svm, genesis.config, &config_state);
    }
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    let unix_0 = 1_000_000i64;
    dvpn_test_support::set_clock(&mut genesis.svm, unix_0);
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec {
            prices: NodePrices {
                channel_price_per_hour: 10_000_000,
                lease_price_per_hour: 5_000_000,
                ..Default::default()
            },
            ..dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), unix_0)
        },
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    let provider_owner = Keypair::new();
    genesis
        .svm
        .airdrop(&provider_owner.pubkey(), 10_000_000_000)
        .unwrap();
    let provider = dvpn_test_support::plant_provider(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [1u8; 32]),
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
    let provider_owner_token = dvpn_test_support::plant_ata(
        &mut genesis.svm,
        genesis.mint,
        &provider_owner.pubkey(),
        100_000_000,
    );

    let owner = Keypair::new();
    genesis
        .svm
        .airdrop(&owner.pubkey(), 10_000_000_000)
        .unwrap();
    let owner_token =
        dvpn_test_support::plant_ata(&mut genesis.svm, genesis.mint, &owner.pubkey(), 200_000_000);

    let redeemer = Keypair::new();
    genesis
        .svm
        .airdrop(&redeemer.pubkey(), 10_000_000_000)
        .unwrap();

    let voucher_channel_id = [10u8; 32];
    let open_voucher_channel = dvpn_test_support::ix_positions::open_channel_ix(
        &genesis,
        &owner.pubkey(),
        &node,
        &owner_token,
        voucher_channel_id,
        1_000_000,
        PositionTerms::Voucher {
            voucher_signer: owner.pubkey(),
        },
        &owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[open_voucher_channel], &[&owner]);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    let (voucher_channel, _) = find_channel(&dvpn::ID, &owner.pubkey(), &voucher_channel_id);
    genesis.svm.expire_blockhash();
    let fund_voucher_channel = dvpn_test_support::ix_positions::fund_channels_ix(
        &genesis,
        &owner.pubkey(),
        &owner_token,
        &[voucher_channel],
        vec![500_000],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[fund_voucher_channel], &[&owner]);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    let voucher_channel_opened_at =
        dvpn_test_support::read_channel(&genesis.svm, &voucher_channel).opened_at;
    let sig = dvpn_test_support::sign_channel_voucher_with_wallet(
        &owner,
        &voucher_channel,
        voucher_channel_opened_at,
        900_000,
    );
    genesis.svm.expire_blockhash();
    let redeem_voucher_channel = dvpn_test_support::ix_positions::redeem_channel_vouchers_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[voucher_channel],
        vec![Voucher {
            cumulative_amount: 900_000,
            signature: sig,
        }],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[redeem_voucher_channel], &[&redeemer]);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    genesis.svm.expire_blockhash();
    let start_close_voucher_channel = dvpn_test_support::ix_positions::start_close_channel_ix(
        &genesis,
        &voucher_channel,
        &owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[start_close_voucher_channel], &[&owner]);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    let unix_1 = unix_0 + 300;
    dvpn_test_support::set_clock(&mut genesis.svm, unix_1);
    genesis.svm.expire_blockhash();
    let close_voucher_channel = dvpn_test_support::ix_positions::close_channel_ix(
        &genesis,
        &voucher_channel,
        &redeemer.pubkey(),
        None,
        &owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[close_voucher_channel], &[&redeemer]);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    let stream_channel_id = [11u8; 32];
    genesis.svm.expire_blockhash();
    let open_stream_channel = dvpn_test_support::ix_positions::open_channel_ix(
        &genesis,
        &owner.pubkey(),
        &node,
        &owner_token,
        stream_channel_id,
        100_000_000,
        PositionTerms::Stream {
            expected_rate_per_hour: 10_000_000,
        },
        &owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[open_stream_channel], &[&owner]);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    let (stream_channel, _) = find_channel(&dvpn::ID, &owner.pubkey(), &stream_channel_id);
    genesis.svm.expire_blockhash();
    let fund_stream_channel = dvpn_test_support::ix_positions::fund_channels_ix(
        &genesis,
        &owner.pubkey(),
        &owner_token,
        &[stream_channel],
        vec![50_000_000],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[fund_stream_channel], &[&owner]);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    let unix_2 = unix_1 + 3_600;
    dvpn_test_support::set_clock(&mut genesis.svm, unix_2);
    genesis.svm.expire_blockhash();
    let redeem_stream_channel = dvpn_test_support::ix_positions::redeem_channel_streams_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[stream_channel],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[redeem_stream_channel], &[&redeemer]);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    let unix_3 = unix_2 + 1_800;
    dvpn_test_support::set_clock(&mut genesis.svm, unix_3);
    genesis.svm.expire_blockhash();
    let close_stream_channel = dvpn_test_support::ix_positions::close_channel_ix(
        &genesis,
        &stream_channel,
        &owner.pubkey(),
        Some(&node),
        &owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[close_stream_channel], &[&owner]);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    let stream_lease_id = [20u8; 32];
    genesis.svm.expire_blockhash();
    let open_stream_lease = dvpn_test_support::ix_positions::open_lease_ix(
        &genesis,
        &provider,
        &node,
        &provider_owner_token,
        stream_lease_id,
        50_000_000,
        PositionTerms::Stream {
            expected_rate_per_hour: 5_000_000,
        },
        &provider_owner.pubkey(),
        &provider_owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[open_stream_lease], &[&provider_owner]);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    let (stream_lease, _) = find_lease(&dvpn::ID, &provider, &stream_lease_id);
    genesis.svm.expire_blockhash();
    let fund_stream_lease = dvpn_test_support::ix_positions::fund_leases_ix(
        &genesis,
        &provider,
        &provider_owner_token,
        &[stream_lease],
        vec![10_000_000],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[fund_stream_lease], &[&provider_owner]);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    let unix_4 = unix_3 + 3_600;
    dvpn_test_support::set_clock(&mut genesis.svm, unix_4);
    genesis.svm.expire_blockhash();
    let redeem_stream_lease = dvpn_test_support::ix_positions::redeem_lease_streams_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[stream_lease],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[redeem_stream_lease], &[&redeemer]);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    let unix_5 = unix_4 + 1_800;
    dvpn_test_support::set_clock(&mut genesis.svm, unix_5);
    genesis.svm.expire_blockhash();
    let close_stream_lease = dvpn_test_support::ix_positions::close_lease_ix(
        &genesis,
        &provider,
        &stream_lease,
        &provider_owner.pubkey(),
        Some(node),
        &provider_owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[close_stream_lease], &[&provider_owner]);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    let voucher_lease_id = [21u8; 32];
    genesis.svm.expire_blockhash();
    let open_voucher_lease = dvpn_test_support::ix_positions::open_lease_ix(
        &genesis,
        &provider,
        &node,
        &provider_owner_token,
        voucher_lease_id,
        2_000_000,
        PositionTerms::Voucher {
            voucher_signer: provider_owner.pubkey(),
        },
        &provider_owner.pubkey(),
        &provider_owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[open_voucher_lease], &[&provider_owner]);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    let (voucher_lease, _) = find_lease(&dvpn::ID, &provider, &voucher_lease_id);
    genesis.svm.expire_blockhash();
    let fund_voucher_lease = dvpn_test_support::ix_positions::fund_leases_ix(
        &genesis,
        &provider,
        &provider_owner_token,
        &[voucher_lease],
        vec![1_000_000],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[fund_voucher_lease], &[&provider_owner]);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    let voucher_lease_opened_at =
        dvpn_test_support::read_lease(&genesis.svm, &voucher_lease).opened_at;
    let sig2 = dvpn_test_support::sign_lease_voucher_with_wallet(
        &provider_owner,
        &voucher_lease,
        voucher_lease_opened_at,
        1_500_000,
    );
    genesis.svm.expire_blockhash();
    let redeem_voucher_lease = dvpn_test_support::ix_positions::redeem_lease_vouchers_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[voucher_lease],
        vec![Voucher {
            cumulative_amount: 1_500_000,
            signature: sig2,
        }],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[redeem_voucher_lease], &[&redeemer]);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    genesis.svm.expire_blockhash();
    let start_close_voucher_lease = dvpn_test_support::ix_positions::start_close_lease_ix(
        &genesis,
        &provider,
        &voucher_lease,
        &provider_owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(
        &mut genesis.svm,
        &[start_close_voucher_lease],
        &[&provider_owner],
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    let unix_6 = unix_5 + 300;
    dvpn_test_support::set_clock(&mut genesis.svm, unix_6);
    genesis.svm.expire_blockhash();
    let close_voucher_lease = dvpn_test_support::ix_positions::close_lease_ix(
        &genesis,
        &provider,
        &voucher_lease,
        &redeemer.pubkey(),
        None,
        &provider_owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[close_voucher_lease], &[&redeemer]);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);

    assert!(dvpn_test_support::is_account_closed(
        &genesis.svm,
        &voucher_channel
    ));
    assert!(dvpn_test_support::is_account_closed(
        &genesis.svm,
        &stream_channel
    ));
    assert!(dvpn_test_support::is_account_closed(
        &genesis.svm,
        &stream_lease
    ));
    assert!(dvpn_test_support::is_account_closed(
        &genesis.svm,
        &voucher_lease
    ));
    let node_state = dvpn_test_support::read_node(&genesis.svm, &node);
    assert_eq!(
        (node_state.revenue, node_state.fees_accrued),
        (24_402_000, 498_000)
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn redeem_after_node_closed_and_reopened_same_owner_id_fails_node_reopened() {
    {
        let mut genesis = dvpn_test_support::genesis();
        let node_owner = Keypair::new();
        let node_id = [55u8; 32];
        let node = dvpn_test_support::plant_node(
            &mut genesis.svm,
            genesis.config,
            &dvpn_test_support::NodeSpec {
                node_id,
                ..dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0)
            },
        );
        let owner = Keypair::new();
        let voucher_signer_key = dvpn_test_support::voucher_signing_key(10);
        let voucher_signer = Pubkey::new_from_array(voucher_signer_key.verifying_key().to_bytes());
        let channel_address = dvpn_test_support::plant_channel(
            &mut genesis.svm,
            genesis.config,
            &dvpn_test_support::ChannelSpec {
                voucher_signer,
                ..dvpn_test_support::ChannelSpec::voucher_on_node(
                    owner.pubkey(),
                    [1u8; 32],
                    node,
                    1_000_000,
                )
            },
        );
        let redeemer = Keypair::new();
        genesis
            .svm
            .airdrop(&redeemer.pubkey(), 10_000_000_000)
            .unwrap();
        dvpn_test_support::assert_solvent(&genesis.svm, 0);

        dvpn_test_support::plant_system_account(&mut genesis.svm, node, 0);
        dvpn_test_support::assert_solvent(&genesis.svm, 0);

        let sig = dvpn_test_support::sign_channel_voucher(
            &voucher_signer_key,
            &channel_address,
            0,
            100_000,
        );
        let vouchers = vec![Voucher {
            cumulative_amount: 100_000,
            signature: sig,
        }];
        let instruction = dvpn_test_support::ix_positions::redeem_channel_vouchers_ix(
            &genesis,
            &redeemer.pubkey(),
            &node,
            &[channel_address],
            vouchers.clone(),
        );
        let failed =
            dvpn_test_support::send_expect_err(&mut genesis.svm, &[instruction], &[&redeemer]);
        dvpn_test_support::assert_anchor_error(
            &failed,
            anchor_lang::error::ErrorCode::AccountNotInitialized,
        );
        assert_eq!(
            dvpn_test_support::read_channel(&genesis.svm, &channel_address).redeemed,
            0
        );
        dvpn_test_support::assert_solvent(&genesis.svm, 0);

        dvpn_test_support::plant_node(
            &mut genesis.svm,
            genesis.config,
            &dvpn_test_support::NodeSpec {
                node_id,
                opened_at: 1,
                ..dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0)
            },
        );
        dvpn_test_support::assert_solvent(&genesis.svm, 0);

        genesis.svm.expire_blockhash();
        let second_instruction = dvpn_test_support::ix_positions::redeem_channel_vouchers_ix(
            &genesis,
            &redeemer.pubkey(),
            &node,
            &[channel_address],
            vouchers,
        );
        let failed = dvpn_test_support::send_expect_err(
            &mut genesis.svm,
            &[second_instruction],
            &[&redeemer],
        );
        dvpn_test_support::assert_dvpn_error(&failed, DvpnError::NodeReopened);
        assert_eq!(
            dvpn_test_support::read_channel(&genesis.svm, &channel_address).redeemed,
            0
        );
        assert_eq!(dvpn_test_support::read_node(&genesis.svm, &node).revenue, 0);
        dvpn_test_support::assert_solvent(&genesis.svm, 0);
    }

    {
        let mut genesis = dvpn_test_support::genesis();
        let node_owner = Keypair::new();
        let node_id = [56u8; 32];
        let node = dvpn_test_support::plant_node(
            &mut genesis.svm,
            genesis.config,
            &dvpn_test_support::NodeSpec {
                node_id,
                ..dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0)
            },
        );
        let owner = Keypair::new();
        let channel_address = dvpn_test_support::plant_channel(
            &mut genesis.svm,
            genesis.config,
            &stream_channel_spec(&owner.pubkey(), [2u8; 32], &node, 10_000, 100, 0),
        );
        let redeemer = Keypair::new();
        genesis
            .svm
            .airdrop(&redeemer.pubkey(), 10_000_000_000)
            .unwrap();
        dvpn_test_support::set_clock(&mut genesis.svm, 3_600);
        dvpn_test_support::assert_solvent(&genesis.svm, 0);

        dvpn_test_support::plant_system_account(&mut genesis.svm, node, 0);
        dvpn_test_support::assert_solvent(&genesis.svm, 0);

        let instruction = dvpn_test_support::ix_positions::redeem_channel_streams_ix(
            &genesis,
            &redeemer.pubkey(),
            &node,
            &[channel_address],
        );
        let failed =
            dvpn_test_support::send_expect_err(&mut genesis.svm, &[instruction], &[&redeemer]);
        dvpn_test_support::assert_anchor_error(
            &failed,
            anchor_lang::error::ErrorCode::AccountNotInitialized,
        );
        assert_eq!(
            dvpn_test_support::read_channel(&genesis.svm, &channel_address).redeemed,
            0
        );
        dvpn_test_support::assert_solvent(&genesis.svm, 0);

        dvpn_test_support::plant_node(
            &mut genesis.svm,
            genesis.config,
            &dvpn_test_support::NodeSpec {
                node_id,
                opened_at: 1,
                ..dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0)
            },
        );
        dvpn_test_support::assert_solvent(&genesis.svm, 0);

        genesis.svm.expire_blockhash();
        let second_instruction = dvpn_test_support::ix_positions::redeem_channel_streams_ix(
            &genesis,
            &redeemer.pubkey(),
            &node,
            &[channel_address],
        );
        let failed = dvpn_test_support::send_expect_err(
            &mut genesis.svm,
            &[second_instruction],
            &[&redeemer],
        );
        dvpn_test_support::assert_dvpn_error(&failed, DvpnError::NodeReopened);
        assert_eq!(
            dvpn_test_support::read_channel(&genesis.svm, &channel_address).redeemed,
            0
        );
        assert_eq!(dvpn_test_support::read_node(&genesis.svm, &node).revenue, 0);
        dvpn_test_support::assert_solvent(&genesis.svm, 0);
    }

    {
        let mut genesis = dvpn_test_support::genesis();
        let node_owner = Keypair::new();
        let node_id = [57u8; 32];
        let node = dvpn_test_support::plant_node(
            &mut genesis.svm,
            genesis.config,
            &dvpn_test_support::NodeSpec {
                node_id,
                ..dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0)
            },
        );
        let provider_owner = Keypair::new();
        let provider = dvpn_test_support::plant_provider(
            &mut genesis.svm,
            genesis.config,
            &dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [3u8; 32]),
        );
        let voucher_signer_key = dvpn_test_support::voucher_signing_key(11);
        let voucher_signer = Pubkey::new_from_array(voucher_signer_key.verifying_key().to_bytes());
        let lease = dvpn_test_support::plant_lease(
            &mut genesis.svm,
            genesis.config,
            &dvpn_test_support::LeaseSpec {
                voucher_signer,
                ..dvpn_test_support::LeaseSpec::voucher_on_node(
                    provider,
                    provider_owner.pubkey(),
                    [4u8; 32],
                    node,
                    1_000_000,
                )
            },
        );
        let redeemer = Keypair::new();
        genesis
            .svm
            .airdrop(&redeemer.pubkey(), 10_000_000_000)
            .unwrap();
        dvpn_test_support::assert_solvent(&genesis.svm, 0);

        dvpn_test_support::plant_system_account(&mut genesis.svm, node, 0);
        dvpn_test_support::assert_solvent(&genesis.svm, 0);

        let sig = dvpn_test_support::sign_lease_voucher(&voucher_signer_key, &lease, 0, 100_000);
        let vouchers = vec![Voucher {
            cumulative_amount: 100_000,
            signature: sig,
        }];
        let instruction = dvpn_test_support::ix_positions::redeem_lease_vouchers_ix(
            &genesis,
            &redeemer.pubkey(),
            &node,
            &[lease],
            vouchers.clone(),
        );
        let failed =
            dvpn_test_support::send_expect_err(&mut genesis.svm, &[instruction], &[&redeemer]);
        dvpn_test_support::assert_anchor_error(
            &failed,
            anchor_lang::error::ErrorCode::AccountNotInitialized,
        );
        assert_eq!(
            dvpn_test_support::read_lease(&genesis.svm, &lease).redeemed,
            0
        );
        dvpn_test_support::assert_solvent(&genesis.svm, 0);

        dvpn_test_support::plant_node(
            &mut genesis.svm,
            genesis.config,
            &dvpn_test_support::NodeSpec {
                node_id,
                opened_at: 1,
                ..dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0)
            },
        );
        dvpn_test_support::assert_solvent(&genesis.svm, 0);

        genesis.svm.expire_blockhash();
        let second_instruction = dvpn_test_support::ix_positions::redeem_lease_vouchers_ix(
            &genesis,
            &redeemer.pubkey(),
            &node,
            &[lease],
            vouchers,
        );
        let failed = dvpn_test_support::send_expect_err(
            &mut genesis.svm,
            &[second_instruction],
            &[&redeemer],
        );
        dvpn_test_support::assert_dvpn_error(&failed, DvpnError::NodeReopened);
        assert_eq!(
            dvpn_test_support::read_lease(&genesis.svm, &lease).redeemed,
            0
        );
        assert_eq!(dvpn_test_support::read_node(&genesis.svm, &node).revenue, 0);
        dvpn_test_support::assert_solvent(&genesis.svm, 0);
    }

    {
        let mut genesis = dvpn_test_support::genesis();
        let node_owner = Keypair::new();
        let node_id = [58u8; 32];
        let node = dvpn_test_support::plant_node(
            &mut genesis.svm,
            genesis.config,
            &dvpn_test_support::NodeSpec {
                node_id,
                ..dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0)
            },
        );
        let provider_owner = Keypair::new();
        let provider = dvpn_test_support::plant_provider(
            &mut genesis.svm,
            genesis.config,
            &dvpn_test_support::ProviderSpec::listed(provider_owner.pubkey(), [5u8; 32]),
        );
        let lease = dvpn_test_support::plant_lease(
            &mut genesis.svm,
            genesis.config,
            &stream_lease_spec(
                &provider,
                &provider_owner.pubkey(),
                [6u8; 32],
                &node,
                10_000,
                100,
                0,
            ),
        );
        let redeemer = Keypair::new();
        genesis
            .svm
            .airdrop(&redeemer.pubkey(), 10_000_000_000)
            .unwrap();
        dvpn_test_support::set_clock(&mut genesis.svm, 3_600);
        dvpn_test_support::assert_solvent(&genesis.svm, 0);

        dvpn_test_support::plant_system_account(&mut genesis.svm, node, 0);
        dvpn_test_support::assert_solvent(&genesis.svm, 0);

        let instruction = dvpn_test_support::ix_positions::redeem_lease_streams_ix(
            &genesis,
            &redeemer.pubkey(),
            &node,
            &[lease],
        );
        let failed =
            dvpn_test_support::send_expect_err(&mut genesis.svm, &[instruction], &[&redeemer]);
        dvpn_test_support::assert_anchor_error(
            &failed,
            anchor_lang::error::ErrorCode::AccountNotInitialized,
        );
        assert_eq!(
            dvpn_test_support::read_lease(&genesis.svm, &lease).redeemed,
            0
        );
        dvpn_test_support::assert_solvent(&genesis.svm, 0);

        dvpn_test_support::plant_node(
            &mut genesis.svm,
            genesis.config,
            &dvpn_test_support::NodeSpec {
                node_id,
                opened_at: 1,
                ..dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0)
            },
        );
        dvpn_test_support::assert_solvent(&genesis.svm, 0);

        genesis.svm.expire_blockhash();
        let second_instruction = dvpn_test_support::ix_positions::redeem_lease_streams_ix(
            &genesis,
            &redeemer.pubkey(),
            &node,
            &[lease],
        );
        let failed = dvpn_test_support::send_expect_err(
            &mut genesis.svm,
            &[second_instruction],
            &[&redeemer],
        );
        dvpn_test_support::assert_dvpn_error(&failed, DvpnError::NodeReopened);
        assert_eq!(
            dvpn_test_support::read_lease(&genesis.svm, &lease).redeemed,
            0
        );
        assert_eq!(dvpn_test_support::read_node(&genesis.svm, &node).revenue, 0);
        dvpn_test_support::assert_solvent(&genesis.svm, 0);
    }
}
