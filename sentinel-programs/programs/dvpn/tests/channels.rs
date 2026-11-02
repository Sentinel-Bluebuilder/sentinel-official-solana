#![allow(clippy::unwrap_used, clippy::indexing_slicing)]
use dvpn::error::DvpnError;
use dvpn_core::args::NodePrices;
use dvpn_core::constants::ALLOWANCE_UNLIMITED;
use dvpn_core::enums::{ListingStatus, PositionMode, PositionTerms};
use dvpn_core::permissions::*;
use dvpn_core::seeds::{find_channel, find_grant, find_user};
use dvpn_core::voucher::Voucher;
use solana_sdk::{pubkey::Pubkey, signature::Keypair, signer::Signer};

fn stream_priced_node_spec(
    owner: &Pubkey,
    listed_at: i64,
    rate: u64,
) -> dvpn_test_support::NodeSpec {
    dvpn_test_support::NodeSpec {
        prices: NodePrices {
            channel_price_per_hour: rate,
            ..Default::default()
        },
        ..dvpn_test_support::NodeSpec::listed(*owner, listed_at)
    }
}

fn stream_channel_spec(
    owner: &Pubkey,
    channel_id: [u8; 32],
    escrow: u64,
    rate_per_hour: u64,
    stream_started_at: i64,
) -> dvpn_test_support::ChannelSpec {
    dvpn_test_support::ChannelSpec {
        mode: PositionMode::Stream as u8,
        rate_per_hour,
        stream_started_at,
        voucher_signer: Pubkey::default(),
        escrow,
        ..dvpn_test_support::ChannelSpec::voucher(*owner, channel_id)
    }
}

#[allow(clippy::too_many_arguments)]
fn stream_redeemable_channel_spec(
    owner: &Pubkey,
    channel_id: [u8; 32],
    node: &Pubkey,
    escrow: u64,
    rate_per_hour: u64,
    stream_started_at: i64,
) -> dvpn_test_support::ChannelSpec {
    dvpn_test_support::ChannelSpec {
        node: *node,
        ..stream_channel_spec(owner, channel_id, escrow, rate_per_hour, stream_started_at)
    }
}

#[test]
fn open_channel_voucher_owner_wallet_funded_sets_user_bump() {
    let mut genesis = dvpn_test_support::genesis();
    let now = 1_700_000_000i64;
    dvpn_test_support::set_clock(&mut genesis.svm, now);
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), now),
    );
    let owner = Keypair::new();
    genesis
        .svm
        .airdrop(&owner.pubkey(), 10_000_000_000)
        .unwrap();
    let owner_token =
        dvpn_test_support::plant_ata(&mut genesis.svm, genesis.mint, &owner.pubkey(), 1_000_000);
    let channel_id = [7u8; 32];

    let voucher_signer_key = dvpn_test_support::voucher_signing_key(2);
    let voucher_signer = Pubkey::new_from_array(voucher_signer_key.verifying_key().to_bytes());
    let instruction = dvpn_test_support::ix_positions::open_channel_ix(
        &genesis,
        &owner.pubkey(),
        &node,
        &owner_token,
        channel_id,
        100_000,
        PositionTerms::Voucher { voucher_signer },
        &owner.pubkey(),
    );
    let meta = dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&owner]);

    let (channel_pda, _) = find_channel(&dvpn::ID, &owner.pubkey(), &channel_id);
    let channel = dvpn_test_support::read_channel(&genesis.svm, &channel_pda);
    assert_eq!(channel.mode, PositionMode::Voucher as u8);
    assert_eq!(
        (channel.escrow, channel.redeemed, channel.rate_per_hour),
        (100_000, 0, 0)
    );
    assert_eq!(channel.opener, owner.pubkey());
    assert_eq!(channel.voucher_signer, voucher_signer);
    let (user_pda, ubump) = find_user(&dvpn::ID, &owner.pubkey());
    let user = dvpn_test_support::read_user(&genesis.svm, &user_pda);
    assert_eq!(user.bump, ubump);
    assert_eq!((user.balance, user.open_channels), (0, 1));
    assert_eq!(
        dvpn_test_support::read_token_amount(&genesis.svm, &genesis.vault),
        100_000
    );
    assert_eq!(
        dvpn_test_support::read_token_amount(&genesis.svm, &owner_token),
        900_000
    );
    dvpn_test_support::assert_event(
        &meta,
        &dvpn::events::UserCreated {
            user: user_pda,
            owner: owner.pubkey(),
        },
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn open_channel_from_user_balance_first_then_wallet() {
    let mut genesis = dvpn_test_support::genesis();
    let now = 1_700_000_000i64;
    dvpn_test_support::set_clock(&mut genesis.svm, now);
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), now),
    );
    let owner = Keypair::new();
    genesis
        .svm
        .airdrop(&owner.pubkey(), 10_000_000_000)
        .unwrap();
    dvpn_test_support::plant_user(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::UserSpec {
            owner: owner.pubkey(),
            balance: 60,
            open_channels: 0,
        },
    );
    let owner_token =
        dvpn_test_support::plant_ata(&mut genesis.svm, genesis.mint, &owner.pubkey(), 1_000_000);
    let channel_id = [17u8; 32];

    let instruction = dvpn_test_support::ix_positions::open_channel_ix(
        &genesis,
        &owner.pubkey(),
        &node,
        &owner_token,
        channel_id,
        100,
        PositionTerms::Voucher {
            voucher_signer: owner.pubkey(),
        },
        &owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&owner]);

    let (user_pda, _) = find_user(&dvpn::ID, &owner.pubkey());
    let user = dvpn_test_support::read_user(&genesis.svm, &user_pda);
    assert_eq!(user.balance, 0);
    assert_eq!(
        dvpn_test_support::read_token_amount(&genesis.svm, &genesis.vault),
        100
    );
    assert_eq!(
        dvpn_test_support::read_token_amount(&genesis.svm, &owner_token),
        999_960
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn open_channel_delisted_node_fails_node_not_listed() {
    let mut genesis = dvpn_test_support::genesis();
    let now = 1_700_000_000i64;
    dvpn_test_support::set_clock(&mut genesis.svm, now);
    let node_owner = Keypair::new();
    let spec = dvpn_test_support::NodeSpec {
        status: ListingStatus::Delisted as u8,
        ..dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), now)
    };
    let node = dvpn_test_support::plant_node(&mut genesis.svm, genesis.config, &spec);
    let owner = Keypair::new();
    genesis
        .svm
        .airdrop(&owner.pubkey(), 10_000_000_000)
        .unwrap();
    let owner_token =
        dvpn_test_support::plant_ata(&mut genesis.svm, genesis.mint, &owner.pubkey(), 1_000_000);
    let channel_id = [19u8; 32];

    let instruction = dvpn_test_support::ix_positions::open_channel_ix(
        &genesis,
        &owner.pubkey(),
        &node,
        &owner_token,
        channel_id,
        0,
        PositionTerms::Voucher {
            voucher_signer: owner.pubkey(),
        },
        &owner.pubkey(),
    );
    let failed = dvpn_test_support::send_expect_err(&mut genesis.svm, &[instruction], &[&owner]);
    dvpn_test_support::assert_dvpn_error(&failed, DvpnError::NodeNotListed);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn open_channel_fails_when_all_paused() {
    let mut genesis = dvpn_test_support::genesis_paused();
    let now = 1_700_000_000i64;
    dvpn_test_support::set_clock(&mut genesis.svm, now);
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), now),
    );
    let owner = Keypair::new();
    genesis
        .svm
        .airdrop(&owner.pubkey(), 10_000_000_000)
        .unwrap();
    let owner_token =
        dvpn_test_support::plant_ata(&mut genesis.svm, genesis.mint, &owner.pubkey(), 1_000_000);
    let channel_id = [21u8; 32];

    let instruction = dvpn_test_support::ix_positions::open_channel_ix(
        &genesis,
        &owner.pubkey(),
        &node,
        &owner_token,
        channel_id,
        0,
        PositionTerms::Voucher {
            voucher_signer: owner.pubkey(),
        },
        &owner.pubkey(),
    );
    let failed = dvpn_test_support::send_expect_err(&mut genesis.svm, &[instruction], &[&owner]);
    dvpn_test_support::assert_dvpn_error(&failed, DvpnError::OperationPaused);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn open_channel_stream_expected_rate_off_by_one_fails_node_price_changed() {
    let mut genesis = dvpn_test_support::genesis();
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &stream_priced_node_spec(&node_owner.pubkey(), 1_000, 10_000_000),
    );
    let owner = Keypair::new();
    genesis
        .svm
        .airdrop(&owner.pubkey(), 10_000_000_000)
        .unwrap();
    let owner_token = dvpn_test_support::plant_ata(
        &mut genesis.svm,
        genesis.mint,
        &owner.pubkey(),
        1_000_000_000,
    );
    dvpn_test_support::set_clock(&mut genesis.svm, 1_000);
    let channel_id = [31u8; 32];

    let instruction = dvpn_test_support::ix_positions::open_channel_ix(
        &genesis,
        &owner.pubkey(),
        &node,
        &owner_token,
        channel_id,
        0,
        PositionTerms::Stream {
            expected_rate_per_hour: 9_999_999,
        },
        &owner.pubkey(),
    );
    let failed = dvpn_test_support::send_expect_err(&mut genesis.svm, &[instruction], &[&owner]);
    dvpn_test_support::assert_dvpn_error(&failed, DvpnError::NodePriceChanged);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn fund_channels_three_items_one_cpi_per_item_events() {
    let mut genesis = dvpn_test_support::genesis();
    let owner = Keypair::new();
    genesis
        .svm
        .airdrop(&owner.pubkey(), 10_000_000_000)
        .unwrap();
    dvpn_test_support::plant_user(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::UserSpec {
            owner: owner.pubkey(),
            balance: 150,
            open_channels: 3,
        },
    );
    let owner_token =
        dvpn_test_support::plant_ata(&mut genesis.svm, genesis.mint, &owner.pubkey(), 1_000);
    let mut channels: Vec<Pubkey> = (0u8..3)
        .map(|seed| {
            dvpn_test_support::plant_channel(
                &mut genesis.svm,
                genesis.config,
                &dvpn_test_support::ChannelSpec::voucher(owner.pubkey(), [seed; 32]),
            )
        })
        .collect();
    channels.sort();
    let instruction = dvpn_test_support::ix_positions::fund_channels_ix(
        &genesis,
        &owner.pubkey(),
        &owner_token,
        &channels,
        vec![100, 100, 100],
    );
    dvpn_test_support::send(&mut genesis.svm, &[instruction], &[&owner]).unwrap();
    assert_eq!(
        dvpn_test_support::read_user(&genesis.svm, &find_user(&dvpn::ID, &owner.pubkey()).0)
            .balance,
        0
    );
    assert_eq!(
        dvpn_test_support::read_token_amount(&genesis.svm, &genesis.vault),
        300
    );
    for channel_address in &channels {
        assert_eq!(
            dvpn_test_support::read_channel(&genesis.svm, channel_address).escrow,
            100
        );
    }
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn fund_channels_second_grantee_of_same_owner_succeeds_with_fund_bit() {
    let mut genesis = dvpn_test_support::genesis();
    let owner = Keypair::new();
    genesis
        .svm
        .airdrop(&owner.pubkey(), 10_000_000_000)
        .unwrap();
    let grantee2 = Keypair::new();
    genesis
        .svm
        .airdrop(&grantee2.pubkey(), 10_000_000_000)
        .unwrap();
    dvpn_test_support::plant_user(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::UserSpec {
            owner: owner.pubkey(),
            balance: 0,
            open_channels: 1,
        },
    );
    let grant2 = dvpn_test_support::plant_grant(
        &mut genesis.svm,
        &dvpn_test_support::GrantSpec {
            granter: owner.pubkey(),
            grantee: grantee2.pubkey(),
            target: Pubkey::default(),
            permissions: PERM_FUND_CHANNELS,
            expires_at: 0,
            allowance: 1_000_000,
        },
    );
    let owner_token =
        dvpn_test_support::plant_ata(&mut genesis.svm, genesis.mint, &owner.pubkey(), 1_000);
    dvpn_test_support::approve_delegate(
        &mut genesis.svm,
        &owner,
        owner_token,
        genesis.mint,
        genesis.config,
        1_000,
    );
    let spec = dvpn_test_support::ChannelSpec::voucher(owner.pubkey(), [44u8; 32]);
    let chan = dvpn_test_support::plant_channel(&mut genesis.svm, genesis.config, &spec);

    let instruction = dvpn_test_support::ix_positions::fund_channels_ix_as_grantee(
        &genesis,
        &owner.pubkey(),
        &grantee2.pubkey(),
        &grant2,
        &owner_token,
        &[chan],
        vec![10],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&grantee2]);
    assert_eq!(
        dvpn_test_support::read_channel(&genesis.svm, &chan).escrow,
        spec.escrow + 10
    );
    assert_eq!(
        dvpn_test_support::read_grant(&genesis.svm, &grant2).allowance,
        1_000_000 - 10
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn redeem_channel_stream_pays_accrued_net_of_fee() {
    let mut genesis = dvpn_test_support::genesis();
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0),
    );
    let owner = Keypair::new();
    let channel = dvpn_test_support::plant_channel(
        &mut genesis.svm,
        genesis.config,
        &stream_redeemable_channel_spec(
            &owner.pubkey(),
            [50u8; 32],
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

    let instruction = dvpn_test_support::ix_positions::redeem_channel_streams_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[channel],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&redeemer]);

    assert_eq!(
        dvpn_test_support::read_node(&genesis.svm, &node).revenue,
        250_000
    );
    assert_eq!(
        dvpn_test_support::read_channel(&genesis.svm, &channel).redeemed,
        250_000
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn redeem_channel_stream_caps_at_escrow() {
    let mut genesis = dvpn_test_support::genesis();
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0),
    );
    let owner = Keypair::new();
    let channel = dvpn_test_support::plant_channel(
        &mut genesis.svm,
        genesis.config,
        &stream_redeemable_channel_spec(&owner.pubkey(), [51u8; 32], &node, 100, 10, 0),
    );
    let redeemer = Keypair::new();
    genesis
        .svm
        .airdrop(&redeemer.pubkey(), 10_000_000_000)
        .unwrap();

    dvpn_test_support::set_clock(&mut genesis.svm, 5 * 3_600);
    let instruction = dvpn_test_support::ix_positions::redeem_channel_streams_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[channel],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&redeemer]);
    assert_eq!(
        dvpn_test_support::read_channel(&genesis.svm, &channel).redeemed,
        50
    );

    genesis.svm.expire_blockhash();
    dvpn_test_support::set_clock(&mut genesis.svm, 7 * 3_600);
    let instruction = dvpn_test_support::ix_positions::redeem_channel_streams_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[channel],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&redeemer]);
    assert_eq!(
        dvpn_test_support::read_channel(&genesis.svm, &channel).redeemed,
        70
    );

    genesis.svm.expire_blockhash();
    dvpn_test_support::set_clock(&mut genesis.svm, 12 * 3_600);
    let instruction = dvpn_test_support::ix_positions::redeem_channel_streams_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[channel],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&redeemer]);
    assert_eq!(
        dvpn_test_support::read_channel(&genesis.svm, &channel).redeemed,
        100
    );

    genesis.svm.expire_blockhash();
    dvpn_test_support::set_clock(&mut genesis.svm, 15 * 3_600);
    let instruction = dvpn_test_support::ix_positions::redeem_channel_streams_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[channel],
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
fn redeem_channel_voucher_by_grantee_pays_net_of_fee() {
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
    let owner = Keypair::new();
    let voucher_signer_key = dvpn_test_support::voucher_signing_key(1);
    let voucher_signer = Pubkey::new_from_array(voucher_signer_key.verifying_key().to_bytes());
    let channel = dvpn_test_support::plant_channel(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ChannelSpec {
            voucher_signer,
            ..dvpn_test_support::ChannelSpec::voucher_on_node(
                owner.pubkey(),
                [9u8; 32],
                node,
                1_000_000,
            )
        },
    );
    let sig = dvpn_test_support::sign_channel_voucher(&voucher_signer_key, &channel, 0, 500_000);
    let vouchers = vec![Voucher {
        cumulative_amount: 500_000,
        signature: sig,
    }];
    let redeemer = Keypair::new();
    genesis
        .svm
        .airdrop(&redeemer.pubkey(), 10_000_000_000)
        .unwrap();
    let instruction = dvpn_test_support::ix_positions::redeem_channel_vouchers_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[channel],
        vouchers,
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&redeemer]);
    let node_state = dvpn_test_support::read_node(&genesis.svm, &node);
    assert_eq!(
        (node_state.revenue, node_state.fees_accrued),
        (490_000, 10_000)
    );
    assert_eq!(
        dvpn_test_support::read_channel(&genesis.svm, &channel).redeemed,
        500_000
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn redeem_channel_voucher_by_second_grantee_fails_signature_invalid() {
    let mut genesis = dvpn_test_support::genesis();
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0),
    );
    let owner = Keypair::new();
    let voucher_signer_key = dvpn_test_support::voucher_signing_key(5);
    let voucher_signer = Pubkey::new_from_array(voucher_signer_key.verifying_key().to_bytes());
    let other_key = dvpn_test_support::voucher_signing_key(6);
    let channel = dvpn_test_support::plant_channel(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ChannelSpec {
            voucher_signer,
            ..dvpn_test_support::ChannelSpec::voucher_on_node(
                owner.pubkey(),
                [13u8; 32],
                node,
                1_000_000,
            )
        },
    );
    let sig = dvpn_test_support::sign_channel_voucher(&other_key, &channel, 0, 100_000);
    let vouchers = vec![Voucher {
        cumulative_amount: 100_000,
        signature: sig,
    }];
    let redeemer = Keypair::new();
    genesis
        .svm
        .airdrop(&redeemer.pubkey(), 10_000_000_000)
        .unwrap();
    let instruction = dvpn_test_support::ix_positions::redeem_channel_vouchers_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &[channel],
        vouchers,
    );
    let failed = dvpn_test_support::send_expect_err(&mut genesis.svm, &[instruction], &[&redeemer]);
    dvpn_test_support::assert_dvpn_error(&failed, DvpnError::InvalidVoucherSignature);
    assert_eq!(
        dvpn_test_support::read_channel(&genesis.svm, &channel).redeemed,
        0
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn redeem_channel_vouchers_over_escrow_aborts_batch() {
    let mut genesis = dvpn_test_support::genesis();
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0),
    );
    let owner = Keypair::new();
    let voucher_signer_key_1 = dvpn_test_support::voucher_signing_key(10);
    let voucher_signer_key_2 = dvpn_test_support::voucher_signing_key(11);
    let ch1 = dvpn_test_support::plant_channel(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ChannelSpec {
            voucher_signer: Pubkey::new_from_array(voucher_signer_key_1.verifying_key().to_bytes()),
            ..dvpn_test_support::ChannelSpec::voucher_on_node(
                owner.pubkey(),
                [16u8; 32],
                node,
                1_000_000,
            )
        },
    );
    let ch2 = dvpn_test_support::plant_channel(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ChannelSpec {
            voucher_signer: Pubkey::new_from_array(voucher_signer_key_2.verifying_key().to_bytes()),
            ..dvpn_test_support::ChannelSpec::voucher_on_node(
                owner.pubkey(),
                [17u8; 32],
                node,
                1_000_000,
            )
        },
    );

    let mut items: Vec<(Pubkey, [u8; 64], u64)> = vec![
        (
            ch1,
            dvpn_test_support::sign_channel_voucher(&voucher_signer_key_1, &ch1, 0, 500_000),
            500_000,
        ),
        (
            ch2,
            dvpn_test_support::sign_channel_voucher(&voucher_signer_key_2, &ch2, 0, 2_000_000),
            2_000_000,
        ),
    ];
    items.sort_by_key(|(channel_address, _, _)| *channel_address);
    let channels: Vec<Pubkey> = items
        .iter()
        .map(|(channel_address, _, _)| *channel_address)
        .collect();
    let vouchers: Vec<Voucher> = items
        .into_iter()
        .map(|(_, signature, cumulative_amount)| Voucher {
            cumulative_amount,
            signature,
        })
        .collect();
    let redeemer = Keypair::new();
    genesis
        .svm
        .airdrop(&redeemer.pubkey(), 10_000_000_000)
        .unwrap();
    let instruction = dvpn_test_support::ix_positions::redeem_channel_vouchers_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &channels,
        vouchers,
    );
    let failed = dvpn_test_support::send_expect_err(&mut genesis.svm, &[instruction], &[&redeemer]);
    dvpn_test_support::assert_dvpn_error(&failed, DvpnError::VoucherExceedsEscrow);
    assert_eq!(
        dvpn_test_support::read_channel(&genesis.svm, &ch1).redeemed,
        0
    );
    assert_eq!(
        dvpn_test_support::read_channel(&genesis.svm, &ch2).redeemed,
        0
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn redeem_channel_voucher_stale_skipped_others_pay() {
    let mut genesis = dvpn_test_support::genesis();
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0),
    );
    let owner = Keypair::new();
    let voucher_signer_key_1 = dvpn_test_support::voucher_signing_key(12);
    let voucher_signer_key_2 = dvpn_test_support::voucher_signing_key(13);
    let ch_stale = dvpn_test_support::plant_channel(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ChannelSpec {
            voucher_signer: Pubkey::new_from_array(voucher_signer_key_1.verifying_key().to_bytes()),
            redeemed: 400_000,
            ..dvpn_test_support::ChannelSpec::voucher_on_node(
                owner.pubkey(),
                [18u8; 32],
                node,
                1_000_000,
            )
        },
    );
    let ch_fresh = dvpn_test_support::plant_channel(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ChannelSpec {
            voucher_signer: Pubkey::new_from_array(voucher_signer_key_2.verifying_key().to_bytes()),
            ..dvpn_test_support::ChannelSpec::voucher_on_node(
                owner.pubkey(),
                [19u8; 32],
                node,
                1_000_000,
            )
        },
    );
    let mut items: Vec<(Pubkey, [u8; 64], u64)> = vec![
        (
            ch_stale,
            dvpn_test_support::sign_channel_voucher(&voucher_signer_key_1, &ch_stale, 0, 300_000),
            300_000,
        ),
        (
            ch_fresh,
            dvpn_test_support::sign_channel_voucher(&voucher_signer_key_2, &ch_fresh, 0, 600_000),
            600_000,
        ),
    ];
    items.sort_by_key(|(channel_address, _, _)| *channel_address);
    let channels: Vec<Pubkey> = items
        .iter()
        .map(|(channel_address, _, _)| *channel_address)
        .collect();
    let vouchers: Vec<Voucher> = items
        .into_iter()
        .map(|(_, signature, cumulative_amount)| Voucher {
            cumulative_amount,
            signature,
        })
        .collect();
    let redeemer = Keypair::new();
    genesis
        .svm
        .airdrop(&redeemer.pubkey(), 10_000_000_000)
        .unwrap();
    let instruction = dvpn_test_support::ix_positions::redeem_channel_vouchers_ix(
        &genesis,
        &redeemer.pubkey(),
        &node,
        &channels,
        vouchers,
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&redeemer]);
    assert_eq!(
        dvpn_test_support::read_channel(&genesis.svm, &ch_stale).redeemed,
        400_000
    );
    assert_eq!(
        dvpn_test_support::read_channel(&genesis.svm, &ch_fresh).redeemed,
        600_000
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn close_channel_voucher_two_stage_credits_user_and_rent() {
    let mut genesis = dvpn_test_support::genesis();
    {
        let mut config_state = dvpn_test_support::read_config(&genesis.svm);
        config_state.channel_close_grace_seconds = 300;
        dvpn_test_support::plant_account(&mut genesis.svm, genesis.config, &config_state);
    }
    let owner = Keypair::new();
    genesis
        .svm
        .airdrop(&owner.pubkey(), 10_000_000_000)
        .unwrap();
    let rent_payer = Keypair::new();
    dvpn_test_support::plant_user(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::UserSpec {
            owner: owner.pubkey(),
            balance: 0,
            open_channels: 1,
        },
    );
    let channel = dvpn_test_support::plant_channel(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ChannelSpec {
            escrow: 100,
            redeemed: 80,
            rent_recipient: rent_payer.pubkey(),
            ..dvpn_test_support::ChannelSpec::voucher(owner.pubkey(), [90u8; 32])
        },
    );
    dvpn_test_support::set_clock(&mut genesis.svm, 10_000);
    let start_ix = dvpn_test_support::ix_positions::start_close_channel_ix(
        &genesis,
        &channel,
        &owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[start_ix], &[&owner]);
    dvpn_test_support::set_clock(&mut genesis.svm, 10_300);
    let stranger = Keypair::new();
    genesis
        .svm
        .airdrop(&stranger.pubkey(), 10_000_000_000)
        .unwrap();
    let close_ix = dvpn_test_support::ix_positions::close_channel_ix(
        &genesis,
        &channel,
        &stranger.pubkey(),
        None,
        &rent_payer.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[close_ix], &[&stranger]);
    assert_eq!(
        dvpn_test_support::read_user(&genesis.svm, &find_user(&dvpn::ID, &owner.pubkey()).0)
            .balance,
        20
    );
    assert!(dvpn_test_support::is_account_closed(&genesis.svm, &channel));
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn close_channel_before_grace_fails_grace_not_elapsed() {
    let mut genesis = dvpn_test_support::genesis();
    {
        let mut config_state = dvpn_test_support::read_config(&genesis.svm);
        config_state.channel_close_grace_seconds = 300;
        dvpn_test_support::plant_account(&mut genesis.svm, genesis.config, &config_state);
    }
    let owner = Keypair::new();
    genesis
        .svm
        .airdrop(&owner.pubkey(), 10_000_000_000)
        .unwrap();
    dvpn_test_support::plant_user(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::UserSpec {
            owner: owner.pubkey(),
            balance: 0,
            open_channels: 2,
        },
    );

    let fully_redeemed_channel = dvpn_test_support::plant_channel(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ChannelSpec {
            escrow: 50,
            redeemed: 50,
            rent_recipient: owner.pubkey(),
            ..dvpn_test_support::ChannelSpec::voucher(owner.pubkey(), [92u8; 32])
        },
    );

    let empty_channel = dvpn_test_support::plant_channel(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ChannelSpec {
            escrow: 0,
            redeemed: 0,
            rent_recipient: owner.pubkey(),
            ..dvpn_test_support::ChannelSpec::voucher(owner.pubkey(), [93u8; 32])
        },
    );
    dvpn_test_support::set_clock(&mut genesis.svm, 10_000);
    let start_full = dvpn_test_support::ix_positions::start_close_channel_ix(
        &genesis,
        &fully_redeemed_channel,
        &owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[start_full], &[&owner]);
    genesis.svm.expire_blockhash();
    let start_zero = dvpn_test_support::ix_positions::start_close_channel_ix(
        &genesis,
        &empty_channel,
        &owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[start_zero], &[&owner]);
    dvpn_test_support::set_clock(&mut genesis.svm, 10_299);
    let signer = Keypair::new();
    genesis
        .svm
        .airdrop(&signer.pubkey(), 10_000_000_000)
        .unwrap();

    let ix_full = dvpn_test_support::ix_positions::close_channel_ix(
        &genesis,
        &fully_redeemed_channel,
        &signer.pubkey(),
        None,
        &owner.pubkey(),
    );
    let failed = dvpn_test_support::send_expect_err(&mut genesis.svm, &[ix_full], &[&signer]);
    dvpn_test_support::assert_dvpn_error(&failed, DvpnError::GraceNotElapsed);

    genesis.svm.expire_blockhash();
    let ix_zero = dvpn_test_support::ix_positions::close_channel_ix(
        &genesis,
        &empty_channel,
        &signer.pubkey(),
        None,
        &owner.pubkey(),
    );
    let failed = dvpn_test_support::send_expect_err(&mut genesis.svm, &[ix_zero], &[&signer]);
    dvpn_test_support::assert_dvpn_error(&failed, DvpnError::GraceNotElapsed);

    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn start_close_channel_by_stranger_fails_unauthorized() {
    let mut genesis = dvpn_test_support::genesis();
    let owner = Keypair::new();
    let stranger = Keypair::new();
    let channel = dvpn_test_support::plant_channel(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ChannelSpec {
            escrow: 100,
            redeemed: 0,
            rent_recipient: owner.pubkey(),
            ..dvpn_test_support::ChannelSpec::voucher(owner.pubkey(), [94u8; 32])
        },
    );
    genesis
        .svm
        .airdrop(&stranger.pubkey(), 10_000_000_000)
        .unwrap();

    let instruction = dvpn_test_support::ix_positions::start_close_channel_ix(
        &genesis,
        &channel,
        &stranger.pubkey(),
    );
    let failed = dvpn_test_support::send_expect_err(&mut genesis.svm, &[instruction], &[&stranger]);
    dvpn_test_support::assert_dvpn_error(&failed, DvpnError::Unauthorized);
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn close_channel_stream_settles_node_in_place() {
    let mut genesis = dvpn_test_support::genesis();
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), 0),
    );
    let owner = Keypair::new();
    genesis
        .svm
        .airdrop(&owner.pubkey(), 10_000_000_000)
        .unwrap();
    dvpn_test_support::plant_user(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::UserSpec {
            owner: owner.pubkey(),
            balance: 0,
            open_channels: 1,
        },
    );
    let channel = dvpn_test_support::plant_channel(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ChannelSpec {
            rent_recipient: owner.pubkey(),
            ..stream_redeemable_channel_spec(
                &owner.pubkey(),
                [100u8; 32],
                &node,
                100_000_000,
                10_000_000,
                1_000,
            )
        },
    );
    dvpn_test_support::set_clock(&mut genesis.svm, 1_000 + 3_600 * 5);
    let close_ix = dvpn_test_support::ix_positions::close_channel_ix(
        &genesis,
        &channel,
        &owner.pubkey(),
        Some(&node),
        &owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[close_ix], &[&owner]);
    assert_eq!(
        dvpn_test_support::read_node(&genesis.svm, &node).revenue,
        50_000_000
    );
    assert_eq!(
        dvpn_test_support::read_user(&genesis.svm, &find_user(&dvpn::ID, &owner.pubkey()).0)
            .balance,
        50_000_000
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn close_channel_stream_after_node_reopen_forfeits_to_payer() {
    let mut genesis = dvpn_test_support::genesis();
    let node_owner = Pubkey::new_unique();
    let node_id = [104u8; 32];

    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec {
            owner: node_owner,
            node_id,
            opened_at: 0,
            ..Default::default()
        },
    );
    let owner = Keypair::new();
    genesis
        .svm
        .airdrop(&owner.pubkey(), 10_000_000_000)
        .unwrap();
    dvpn_test_support::plant_user(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::UserSpec {
            owner: owner.pubkey(),
            balance: 0,
            open_channels: 1,
        },
    );
    let channel = dvpn_test_support::plant_channel(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::ChannelSpec {
            rent_recipient: owner.pubkey(),
            node_opened_at: 0,
            ..stream_redeemable_channel_spec(&owner.pubkey(), [105u8; 32], &node, 1_000, 10, 0)
        },
    );

    dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec {
            owner: node_owner,
            node_id,
            opened_at: 5,
            ..Default::default()
        },
    );
    dvpn_test_support::set_clock(&mut genesis.svm, 1_000 * 3_600);
    let close_ix = dvpn_test_support::ix_positions::close_channel_ix(
        &genesis,
        &channel,
        &owner.pubkey(),
        Some(&node),
        &owner.pubkey(),
    );
    let meta = dvpn_test_support::send_expect_ok(&mut genesis.svm, &[close_ix], &[&owner]);
    assert_eq!(
        dvpn_test_support::read_user(&genesis.svm, &find_user(&dvpn::ID, &owner.pubkey()).0)
            .balance,
        1_000
    );
    assert_eq!(dvpn_test_support::read_node(&genesis.svm, &node).revenue, 0);
    dvpn_test_support::assert_event(
        &meta,
        &dvpn::events::ChannelClosed {
            channel,
            owner: owner.pubkey(),
            node,
            mode: PositionMode::Stream as u8,
            escrow: 1_000,
            redeemed: 0,
            returned: 1_000,
            forfeited: 1_000,
            user_balance: 1_000,
        },
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn close_channel_and_withdraw_user_balance_refunds_rent_recipients() {
    let mut genesis = dvpn_test_support::genesis();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &stream_priced_node_spec(&Pubkey::new_unique(), 0, 10),
    );
    let owner = dvpn_test_support::funded_keypair(&mut genesis.svm);
    let rent_payer = dvpn_test_support::funded_keypair(&mut genesis.svm);
    let owner_token =
        dvpn_test_support::plant_ata(&mut genesis.svm, genesis.mint, &owner.pubkey(), 1_000);
    let rent_payer_before = dvpn_test_support::read_lamports(&genesis.svm, &rent_payer.pubkey());

    let open = dvpn_test_support::ix_positions::open_channel_ix_by(
        &owner.pubkey(),
        None,
        &genesis,
        &owner.pubkey(),
        &node,
        &owner_token,
        [7u8; 32],
        1_000,
        PositionTerms::Stream {
            expected_rate_per_hour: 10,
        },
        &rent_payer.pubkey(),
        &owner.pubkey(),
        &rent_payer.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[open], &[&owner, &rent_payer]);
    let (channel, _) = find_channel(&dvpn::ID, &owner.pubkey(), &[7u8; 32]);
    let (user, _) = find_user(&dvpn::ID, &owner.pubkey());
    let channel_rent = dvpn_test_support::read_lamports(&genesis.svm, &channel);
    let user_rent = dvpn_test_support::read_lamports(&genesis.svm, &user);
    assert_eq!(
        dvpn_test_support::read_lamports(&genesis.svm, &rent_payer.pubkey()),
        rent_payer_before - channel_rent - user_rent
    );

    let owner_before = dvpn_test_support::read_lamports(&genesis.svm, &owner.pubkey());
    let close = dvpn_test_support::ix_positions::close_channel_ix(
        &genesis,
        &channel,
        &owner.pubkey(),
        Some(&node),
        &owner.pubkey(),
    );
    let close_fee = dvpn_test_support::send_expect_ok(&mut genesis.svm, &[close], &[&owner]).fee;
    assert_eq!(
        dvpn_test_support::read_lamports(&genesis.svm, &owner.pubkey()),
        owner_before + channel_rent - close_fee
    );

    let withdraw = dvpn_test_support::ix_user::withdraw_user_balance_ix_by(
        &owner.pubkey(),
        None,
        &owner.pubkey(),
        rent_payer.pubkey(),
        owner_token,
        genesis.mint,
    );
    let withdraw_fee =
        dvpn_test_support::send_expect_ok(&mut genesis.svm, &[withdraw], &[&owner]).fee;
    assert_eq!(
        dvpn_test_support::read_lamports(&genesis.svm, &owner.pubkey()),
        owner_before + channel_rent - close_fee - withdraw_fee
    );
    assert_eq!(
        dvpn_test_support::read_lamports(&genesis.svm, &rent_payer.pubkey()),
        rent_payer_before - channel_rent
    );
}

#[test]
fn open_channel_keeps_existing_user_rent_recipient() {
    let mut genesis = dvpn_test_support::genesis();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &stream_priced_node_spec(&Pubkey::new_unique(), 0, 10),
    );
    let owner = dvpn_test_support::funded_keypair(&mut genesis.svm);
    let rent_payer = dvpn_test_support::funded_keypair(&mut genesis.svm);
    let owner_token =
        dvpn_test_support::plant_ata(&mut genesis.svm, genesis.mint, &owner.pubkey(), 1_000);
    let user = dvpn_test_support::plant_user(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::UserSpec {
            owner: owner.pubkey(),
            ..Default::default()
        },
    );

    let open = dvpn_test_support::ix_positions::open_channel_ix_by(
        &owner.pubkey(),
        None,
        &genesis,
        &owner.pubkey(),
        &node,
        &owner_token,
        [7u8; 32],
        1_000,
        PositionTerms::Stream {
            expected_rate_per_hour: 10,
        },
        &rent_payer.pubkey(),
        &owner.pubkey(),
        &rent_payer.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[open], &[&owner, &rent_payer]);

    assert_eq!(
        dvpn_test_support::read_user(&genesis.svm, &user).rent_recipient,
        owner.pubkey()
    );
}

#[test]
fn grantee_opened_channel_closes_by_third_party_after_grace_and_grant_closable() {
    let mut genesis = dvpn_test_support::genesis();
    {
        let mut config_state = dvpn_test_support::read_config(&genesis.svm);
        config_state.channel_close_grace_seconds = 300;
        dvpn_test_support::plant_account(&mut genesis.svm, genesis.config, &config_state);
    }
    let now = 1_700_000_000i64;
    dvpn_test_support::set_clock(&mut genesis.svm, now);
    let node_owner = Keypair::new();
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec::listed(node_owner.pubkey(), now),
    );
    let owner = Keypair::new();
    genesis
        .svm
        .airdrop(&owner.pubkey(), 10_000_000_000)
        .unwrap();
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
        &owner.pubkey(),
        &rent_payer.pubkey(),
        grantee.pubkey(),
        Pubkey::default(),
        PERM_OPEN_CHANNEL,
        0,
        ALLOWANCE_UNLIMITED,
        rent_payer.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[open_grant], &[&owner, &rent_payer]);
    let grant = find_grant(
        &dvpn::ID,
        &owner.pubkey(),
        &grantee.pubkey(),
        &Pubkey::default(),
    )
    .0;
    let owner_token =
        dvpn_test_support::plant_ata(&mut genesis.svm, genesis.mint, &owner.pubkey(), 0);
    let channel_id = [120u8; 32];

    let open = dvpn_test_support::ix_positions::open_channel_ix_as_grantee_with_accounts(
        genesis.config,
        genesis.vault,
        genesis.mint,
        &owner.pubkey(),
        &grantee.pubkey(),
        &grant,
        &node,
        &owner_token,
        channel_id,
        0,
        PositionTerms::Voucher {
            voucher_signer: grantee.pubkey(),
        },
        &grantee.pubkey(),
        &grantee.pubkey(),
        &grantee.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[open], &[&grantee]);
    let (channel, _) = find_channel(&dvpn::ID, &owner.pubkey(), &channel_id);

    genesis.svm.expire_blockhash();
    let start = dvpn_test_support::ix_positions::start_close_channel_ix(
        &genesis,
        &channel,
        &owner.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[start], &[&owner]);
    dvpn_test_support::set_clock(&mut genesis.svm, now + 300);
    let stranger = Keypair::new();
    genesis
        .svm
        .airdrop(&stranger.pubkey(), 10_000_000_000)
        .unwrap();

    genesis.svm.expire_blockhash();
    let close = dvpn_test_support::ix_positions::close_channel_ix(
        &genesis,
        &channel,
        &stranger.pubkey(),
        None,
        &grantee.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[close], &[&stranger]);
    assert!(dvpn_test_support::is_account_closed(&genesis.svm, &channel));

    genesis.svm.expire_blockhash();
    let close_grant =
        dvpn_test_support::ix_grant::close_grant_ix(&owner.pubkey(), grant, rent_payer.pubkey());
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[close_grant], &[&owner]);
    assert!(dvpn_test_support::read_grant_if_exists(&genesis.svm, &grant).is_none());
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}
