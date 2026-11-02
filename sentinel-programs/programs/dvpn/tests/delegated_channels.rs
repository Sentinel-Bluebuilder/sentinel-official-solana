#![allow(clippy::unwrap_used, clippy::indexing_slicing)]
use dvpn::error::DvpnError;
use dvpn_core::args::NodePrices;
use dvpn_core::enums::PositionTerms;
use dvpn_core::permissions::*;
use dvpn_core::seeds::find_channel;
use dvpn_core::voucher::Voucher;
use dvpn_test_support::ix_grant;
use dvpn_test_support::*;
use solana_sdk::pubkey::Pubkey;
use solana_sdk::signature::Signer;

#[test]
fn zero_allowance_grant_cannot_open_channel_and_vault_is_untouched() {
    let mut genesis = genesis();
    let now = 1_700_000_000i64;
    set_clock(&mut genesis.svm, now);
    let node = plant_node(
        &mut genesis.svm,
        genesis.config,
        &NodeSpec {
            node_id: [1u8; 32],
            prices: NodePrices {
                channel_price_per_hour: 0,
                ..Default::default()
            },
            ..NodeSpec::listed(Pubkey::new_unique(), now)
        },
    );
    let owner = funded_keypair(&mut genesis.svm);
    let grantee = funded_keypair(&mut genesis.svm);
    let owner_token = plant_ata(&mut genesis.svm, genesis.mint, &owner.pubkey(), 1_000);
    approve_delegate(
        &mut genesis.svm,
        &owner,
        owner_token,
        genesis.mint,
        genesis.config,
        1_000,
    );
    let grant = open_unscoped_grant(
        &mut genesis.svm,
        &owner,
        grantee.pubkey(),
        PERM_OPEN_CHANNEL,
        0,
    );
    let instruction = ix_positions::open_channel_ix_as_grantee_with_accounts(
        genesis.config,
        genesis.vault,
        genesis.mint,
        &owner.pubkey(),
        &grantee.pubkey(),
        &grant,
        &node,
        &owner_token,
        [9u8; 32],
        1,
        PositionTerms::Voucher {
            voucher_signer: grantee.pubkey(),
        },
        &grantee.pubkey(),
        &grantee.pubkey(),
        &grantee.pubkey(),
    );
    assert_dvpn_error(
        &send_expect_err(&mut genesis.svm, &[instruction], &[&grantee]),
        DvpnError::AmountExceedsAllowance,
    );
    assert_eq!(read_token_amount(&genesis.svm, &genesis.vault), 0);
}

#[test]
fn scoped_fund_batch_fails_whole_when_one_channel_is_off_target() {
    let mut genesis = genesis();
    let (node_a, node_b) = (Pubkey::new_unique(), Pubkey::new_unique());
    let owner = funded_keypair(&mut genesis.svm);
    let grantee = funded_keypair(&mut genesis.svm);
    let rent_payer = funded_keypair(&mut genesis.svm);
    plant_user(
        &mut genesis.svm,
        genesis.config,
        &UserSpec {
            owner: owner.pubkey(),
            balance: 0,
            open_channels: 2,
        },
    );
    let owner_token = plant_ata(&mut genesis.svm, genesis.mint, &owner.pubkey(), 1_000);
    approve_delegate(
        &mut genesis.svm,
        &owner,
        owner_token,
        genesis.mint,
        genesis.config,
        1_000,
    );
    let channel_pda =
        |channel_id: [u8; 32]| find_channel(&dvpn::ID, &owner.pubkey(), &channel_id).0;
    let (id_a, id_b) = if channel_pda([3u8; 32]) < channel_pda([4u8; 32]) {
        ([3u8; 32], [4u8; 32])
    } else {
        ([4u8; 32], [3u8; 32])
    };
    let spec = |channel_id: [u8; 32], node: Pubkey| ChannelSpec {
        owner: owner.pubkey(),
        channel_id,
        node,
        opener: owner.pubkey(),
        escrow: 5,
        rent_recipient: owner.pubkey(),
        voucher_signer: owner.pubkey(),
        ..Default::default()
    };
    let channels = [
        plant_channel(&mut genesis.svm, genesis.config, &spec(id_a, node_a)),
        plant_channel(&mut genesis.svm, genesis.config, &spec(id_b, node_b)),
    ];
    let open = ix_grant::open_grant_ix(
        &owner.pubkey(),
        &rent_payer.pubkey(),
        grantee.pubkey(),
        node_a,
        PERM_FUND_CHANNELS,
        0,
        1_000,
        rent_payer.pubkey(),
    );
    send(&mut genesis.svm, &[open], &[&owner, &rent_payer]).unwrap();
    let scoped = find_grant_address(owner.pubkey(), grantee.pubkey(), node_a);
    let instruction = ix_positions::fund_channels_ix_as_grantee(
        &genesis,
        &owner.pubkey(),
        &grantee.pubkey(),
        &scoped,
        &owner_token,
        &channels,
        vec![1, 1],
    );
    assert_dvpn_error(
        &send_expect_err(&mut genesis.svm, &[instruction], &[&grantee]),
        DvpnError::GrantTargetMismatch,
    );
    for channel_address in &channels {
        assert_eq!(read_channel(&genesis.svm, channel_address).escrow, 5);
    }
}

#[test]
fn second_grantee_closes_stream_channel_opened_by_first() {
    let mut genesis = genesis();
    let now = 1_700_000_000i64;
    set_clock(&mut genesis.svm, now);
    let node = plant_node(
        &mut genesis.svm,
        genesis.config,
        &NodeSpec {
            node_id: [1u8; 32],
            prices: NodePrices {
                channel_price_per_hour: 10,
                ..Default::default()
            },
            ..NodeSpec::listed(Pubkey::new_unique(), now)
        },
    );
    let owner = funded_keypair(&mut genesis.svm);
    let grantee_a = funded_keypair(&mut genesis.svm);
    let grantee_b = funded_keypair(&mut genesis.svm);
    let owner_token = plant_ata(&mut genesis.svm, genesis.mint, &owner.pubkey(), 0);
    let grant_a = open_unscoped_grant(
        &mut genesis.svm,
        &owner,
        grantee_a.pubkey(),
        PERM_OPEN_CHANNEL,
        0,
    );
    let grant_b = open_unscoped_grant(
        &mut genesis.svm,
        &owner,
        grantee_b.pubkey(),
        PERM_CLOSE_CHANNEL,
        0,
    );
    let open = ix_positions::open_channel_ix_as_grantee_with_accounts(
        genesis.config,
        genesis.vault,
        genesis.mint,
        &owner.pubkey(),
        &grantee_a.pubkey(),
        &grant_a,
        &node,
        &owner_token,
        [5u8; 32],
        0,
        PositionTerms::Stream {
            expected_rate_per_hour: 10,
        },
        &grantee_a.pubkey(),
        &grantee_a.pubkey(),
        &grantee_a.pubkey(),
    );
    send(&mut genesis.svm, &[open], &[&grantee_a]).unwrap();
    let channel = find_channel(&dvpn::ID, &owner.pubkey(), &[5u8; 32]).0;
    assert_eq!(
        read_channel(&genesis.svm, &channel).voucher_signer,
        Pubkey::default()
    );

    let close = ix_positions::close_channel_ix_by(
        &grantee_b.pubkey(),
        Some(grant_b),
        &genesis,
        &channel,
        Some(&node),
        &grantee_a.pubkey(),
    );
    send(&mut genesis.svm, &[close], &[&grantee_b]).unwrap();
    assert!(is_account_closed(&genesis.svm, &channel));
    assert_solvent(&genesis.svm, 0);
}

#[test]
fn grantee_names_the_voucher_signer_and_the_node_redeems() {
    let mut genesis = genesis();
    let now = 1_700_000_000i64;
    set_clock(&mut genesis.svm, now);
    let node_owner = funded_keypair(&mut genesis.svm);
    let node = plant_node(
        &mut genesis.svm,
        genesis.config,
        &NodeSpec {
            node_id: [1u8; 32],
            prices: NodePrices {
                channel_price_per_hour: 0,
                ..Default::default()
            },
            ..NodeSpec::listed(node_owner.pubkey(), now)
        },
    );
    let owner = funded_keypair(&mut genesis.svm);
    let grantee = funded_keypair(&mut genesis.svm);
    let voucher_signer_key = voucher_signing_key(70);
    let voucher_signer = Pubkey::new_from_array(voucher_signer_key.verifying_key().to_bytes());
    let owner_token = plant_ata(&mut genesis.svm, genesis.mint, &owner.pubkey(), 1_000);
    approve_delegate(
        &mut genesis.svm,
        &owner,
        owner_token,
        genesis.mint,
        genesis.config,
        1_000,
    );
    let grant = open_unscoped_grant(
        &mut genesis.svm,
        &owner,
        grantee.pubkey(),
        PERM_OPEN_CHANNEL,
        1_000,
    );

    let open = ix_positions::open_channel_ix_as_grantee_with_accounts(
        genesis.config,
        genesis.vault,
        genesis.mint,
        &owner.pubkey(),
        &grantee.pubkey(),
        &grant,
        &node,
        &owner_token,
        [6u8; 32],
        1_000,
        PositionTerms::Voucher { voucher_signer },
        &grantee.pubkey(),
        &grantee.pubkey(),
        &grantee.pubkey(),
    );
    send_expect_ok(&mut genesis.svm, &[open], &[&grantee]);
    let channel = find_channel(&dvpn::ID, &owner.pubkey(), &[6u8; 32]).0;
    let state = read_channel(&genesis.svm, &channel);
    assert_eq!(state.opener, grantee.pubkey());
    assert_eq!(state.voucher_signer, voucher_signer);

    let sig = sign_channel_voucher(&voucher_signer_key, &channel, state.opened_at, 400);
    let redeem = ix_positions::redeem_channel_vouchers_ix(
        &genesis,
        &node_owner.pubkey(),
        &node,
        &[channel],
        vec![Voucher {
            cumulative_amount: 400,
            signature: sig,
        }],
    );
    send_expect_ok(&mut genesis.svm, &[redeem], &[&node_owner]);
    assert_eq!(read_channel(&genesis.svm, &channel).redeemed, 400);
    assert_solvent(&genesis.svm, 0);
}
