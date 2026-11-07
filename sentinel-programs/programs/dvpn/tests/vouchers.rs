#![allow(clippy::unwrap_used, clippy::indexing_slicing)]
use dvpn::error::DvpnError;
use dvpn_core::enums::{ListingStatus, PositionMode, PositionStatus, PositionTerms};
use dvpn_core::seeds::{find_channel, find_lease, find_user};
use dvpn_core::voucher::{CHANNEL_VOUCHER_DOMAIN, LEASE_VOUCHER_DOMAIN, PartialVoucher, Voucher};
use solana_sdk::hash::hash;
use solana_sdk::{instruction::Instruction, pubkey::Pubkey, signature::Keypair, signer::Signer};

#[test]
fn channel_voucher_domain_is_sha256_of_name() {
    assert_eq!(
        CHANNEL_VOUCHER_DOMAIN,
        hash(b"sentinel-dvpn/channel-voucher/v1").to_bytes()
    );
}

#[test]
fn lease_voucher_domain_is_sha256_of_name() {
    assert_eq!(
        LEASE_VOUCHER_DOMAIN,
        hash(b"sentinel-dvpn/lease-voucher/v1").to_bytes()
    );
}

#[derive(Clone, Copy)]
enum PositionKind {
    Channel,
    Lease,
}

impl PositionKind {
    fn sign(
        self,
        key: &dvpn_test_support::SigningKey,
        position: &Pubkey,
        opened_at: i64,
        cumulative_amount: u64,
    ) -> [u8; 64] {
        match self {
            PositionKind::Channel => {
                dvpn_test_support::sign_channel_voucher(key, position, opened_at, cumulative_amount)
            }
            PositionKind::Lease => {
                dvpn_test_support::sign_lease_voucher(key, position, opened_at, cumulative_amount)
            }
        }
    }

    fn sign_with_wallet(
        self,
        wallet: &Keypair,
        position: &Pubkey,
        opened_at: i64,
        cumulative_amount: u64,
    ) -> [u8; 64] {
        match self {
            PositionKind::Channel => dvpn_test_support::sign_channel_voucher_with_wallet(
                wallet,
                position,
                opened_at,
                cumulative_amount,
            ),
            PositionKind::Lease => dvpn_test_support::sign_lease_voucher_with_wallet(
                wallet,
                position,
                opened_at,
                cumulative_amount,
            ),
        }
    }
}

#[allow(clippy::too_many_arguments)]
fn plant_position(
    genesis: &mut dvpn_test_support::Genesis,
    kind: PositionKind,
    node: &Pubkey,
    position_id: [u8; 32],
    owner: Pubkey,
    opener: Pubkey,
    voucher_signer: Pubkey,
    escrow: u64,
) -> Pubkey {
    match kind {
        PositionKind::Channel => dvpn_test_support::plant_channel(
            &mut genesis.svm,
            genesis.config,
            &dvpn_test_support::ChannelSpec {
                owner,
                channel_id: position_id,
                node: *node,
                opener,
                voucher_signer,
                mode: PositionMode::Voucher as u8,
                status: PositionStatus::Open as u8,
                escrow,
                ..Default::default()
            },
        ),
        PositionKind::Lease => dvpn_test_support::plant_lease(
            &mut genesis.svm,
            genesis.config,
            &dvpn_test_support::LeaseSpec {
                provider: Pubkey::new_unique(),
                lease_id: position_id,
                node: *node,
                owner,
                opener,
                voucher_signer,
                mode: PositionMode::Voucher as u8,
                status: PositionStatus::Open as u8,
                escrow,
                ..Default::default()
            },
        ),
    }
}

fn redeem_ix(
    genesis: &dvpn_test_support::Genesis,
    kind: PositionKind,
    redeemer: &Pubkey,
    node: &Pubkey,
    positions: &[Pubkey],
    vouchers: Vec<Voucher>,
) -> Instruction {
    match kind {
        PositionKind::Channel => dvpn_test_support::ix_positions::redeem_channel_vouchers_ix(
            genesis, redeemer, node, positions, vouchers,
        ),
        PositionKind::Lease => dvpn_test_support::ix_positions::redeem_lease_vouchers_ix(
            genesis, redeemer, node, positions, vouchers,
        ),
    }
}

fn plant_listed_node(genesis: &mut dvpn_test_support::Genesis) -> Pubkey {
    let owner = Keypair::new();
    dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec {
            owner: owner.pubkey(),
            status: ListingStatus::Listed as u8,
            ..Default::default()
        },
    )
}

fn assert_redeem_fails_with(
    genesis: &mut dvpn_test_support::Genesis,
    kind: PositionKind,
    node: &Pubkey,
    position: Pubkey,
    voucher: Voucher,
    err: DvpnError,
) {
    let redeemer = Keypair::new();
    genesis
        .svm
        .airdrop(&redeemer.pubkey(), 10_000_000_000)
        .unwrap();
    let instruction = redeem_ix(
        genesis,
        kind,
        &redeemer.pubkey(),
        node,
        &[position],
        vec![voucher],
    );
    let failed = dvpn_test_support::send_expect_err(&mut genesis.svm, &[instruction], &[&redeemer]);
    dvpn_test_support::assert_dvpn_error(&failed, err);
}

#[test]
fn verifier_valid_voucher_on_other_position_same_node_and_other_node_fails() {
    for kind in [PositionKind::Channel, PositionKind::Lease] {
        let mut genesis = dvpn_test_support::genesis();
        let node1 = plant_listed_node(&mut genesis);
        let node2 = plant_listed_node(&mut genesis);
        let voucher_signer_key = dvpn_test_support::voucher_signing_key(30);
        let voucher_signer = Pubkey::new_from_array(voucher_signer_key.verifying_key().to_bytes());

        let position_a = plant_position(
            &mut genesis,
            kind,
            &node1,
            [9u8; 32],
            Pubkey::new_unique(),
            voucher_signer,
            voucher_signer,
            1_000_000,
        );
        let sig = kind.sign(&voucher_signer_key, &position_a, 0, 500);
        let voucher = Voucher {
            cumulative_amount: 500,
            signature: sig,
        };

        let position_b = plant_position(
            &mut genesis,
            kind,
            &node1,
            [10u8; 32],
            Pubkey::new_unique(),
            voucher_signer,
            voucher_signer,
            1_000_000,
        );
        assert_redeem_fails_with(
            &mut genesis,
            kind,
            &node1,
            position_b,
            voucher.clone(),
            DvpnError::InvalidVoucherSignature,
        );

        let position_c = plant_position(
            &mut genesis,
            kind,
            &node2,
            [11u8; 32],
            Pubkey::new_unique(),
            voucher_signer,
            voucher_signer,
            1_000_000,
        );
        assert_redeem_fails_with(
            &mut genesis,
            kind,
            &node2,
            position_c,
            voucher,
            DvpnError::InvalidVoucherSignature,
        );

        dvpn_test_support::assert_solvent(&genesis.svm, 0);
    }
}

#[test]
fn verifier_lease_voucher_as_channel_voucher_and_reverse_fail() {
    let mut genesis = dvpn_test_support::genesis();
    let node = plant_listed_node(&mut genesis);
    let voucher_signer_key = dvpn_test_support::voucher_signing_key(40);
    let voucher_signer = Pubkey::new_from_array(voucher_signer_key.verifying_key().to_bytes());

    let channel = plant_position(
        &mut genesis,
        PositionKind::Channel,
        &node,
        [12u8; 32],
        Pubkey::new_unique(),
        voucher_signer,
        voucher_signer,
        1_000_000,
    );
    let sig = dvpn_test_support::sign_lease_voucher(&voucher_signer_key, &channel, 0, 500);
    let voucher = Voucher {
        cumulative_amount: 500,
        signature: sig,
    };
    assert_redeem_fails_with(
        &mut genesis,
        PositionKind::Channel,
        &node,
        channel,
        voucher,
        DvpnError::InvalidVoucherSignature,
    );

    let lease = plant_position(
        &mut genesis,
        PositionKind::Lease,
        &node,
        [13u8; 32],
        Pubkey::new_unique(),
        voucher_signer,
        voucher_signer,
        1_000_000,
    );
    let sig2 = dvpn_test_support::sign_channel_voucher(&voucher_signer_key, &lease, 0, 500);
    let voucher2 = Voucher {
        cumulative_amount: 500,
        signature: sig2,
    };
    assert_redeem_fails_with(
        &mut genesis,
        PositionKind::Lease,
        &node,
        lease,
        voucher2,
        DvpnError::InvalidVoucherSignature,
    );

    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn stale_channel_voucher_fails_after_same_id_reopen() {
    let mut genesis = dvpn_test_support::genesis();
    dvpn_test_support::set_clock(&mut genesis.svm, 10_000);
    let node_owner = Keypair::new();
    genesis
        .svm
        .airdrop(&node_owner.pubkey(), 10_000_000_000)
        .unwrap();
    let node_id = [61u8; 32];
    let node = dvpn_test_support::plant_node(
        &mut genesis.svm,
        genesis.config,
        &dvpn_test_support::NodeSpec {
            owner: node_owner.pubkey(),
            node_id,
            status: ListingStatus::Listed as u8,
            listed_at: 10_000,
            bond: 1_000,
            rent_recipient: node_owner.pubkey(),
            opened_at: 10_000,
            ..Default::default()
        },
    );
    let user = Keypair::new();
    genesis.svm.airdrop(&user.pubkey(), 10_000_000_000).unwrap();
    let user_token =
        dvpn_test_support::plant_ata(&mut genesis.svm, genesis.mint, &user.pubkey(), 1_000);
    let channel_id = [42u8; 32];
    let (channel, _) = find_channel(&dvpn::ID, &user.pubkey(), &channel_id);

    let open = dvpn_test_support::ix_positions::open_channel_ix(
        &genesis,
        &user.pubkey(),
        &node,
        &user_token,
        channel_id,
        100,
        PositionTerms::Voucher {
            voucher_signer: user.pubkey(),
        },
        &user.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[open], &[&user]);
    let opened_at = dvpn_test_support::read_channel(&genesis.svm, &channel).opened_at;

    let sig = dvpn_test_support::sign_channel_voucher_with_wallet(&user, &channel, opened_at, 100);
    let voucher = Voucher {
        cumulative_amount: 100,
        signature: sig,
    };
    let redeem = dvpn_test_support::ix_positions::redeem_channel_vouchers_ix(
        &genesis,
        &node_owner.pubkey(),
        &node,
        &[channel],
        vec![voucher.clone()],
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[redeem], &[&node_owner]);
    assert_eq!(
        dvpn_test_support::read_node(&genesis.svm, &node).revenue,
        100
    );

    genesis.svm.expire_blockhash();
    let start =
        dvpn_test_support::ix_positions::start_close_channel_ix(&genesis, &channel, &user.pubkey());
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[start], &[&user]);
    dvpn_test_support::set_clock(&mut genesis.svm, 10_300);
    genesis.svm.expire_blockhash();
    let close = dvpn_test_support::ix_positions::close_channel_ix(
        &genesis,
        &channel,
        &user.pubkey(),
        None,
        &user.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[close], &[&user]);
    assert!(dvpn_test_support::is_account_closed(&genesis.svm, &channel));

    genesis.svm.expire_blockhash();
    let relist = dvpn_test_support::ix_node::list_node_ix(&node_owner.pubkey(), node_id);
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[relist], &[&node_owner]);
    dvpn_test_support::set_clock(&mut genesis.svm, 10_301);
    genesis.svm.expire_blockhash();
    let reopen = dvpn_test_support::ix_positions::open_channel_ix(
        &genesis,
        &user.pubkey(),
        &node,
        &user_token,
        channel_id,
        100,
        PositionTerms::Voucher {
            voucher_signer: user.pubkey(),
        },
        &user.pubkey(),
    );
    dvpn_test_support::send_expect_ok(&mut genesis.svm, &[reopen], &[&user]);
    assert_ne!(
        dvpn_test_support::read_channel(&genesis.svm, &channel).opened_at,
        opened_at
    );

    genesis.svm.expire_blockhash();
    let replay = dvpn_test_support::ix_positions::redeem_channel_vouchers_ix(
        &genesis,
        &node_owner.pubkey(),
        &node,
        &[channel],
        vec![voucher],
    );
    let failed = dvpn_test_support::send_expect_err(&mut genesis.svm, &[replay], &[&node_owner]);
    dvpn_test_support::assert_dvpn_error(&failed, DvpnError::InvalidVoucherSignature);
    assert_eq!(
        dvpn_test_support::read_channel(&genesis.svm, &channel).redeemed,
        0
    );
    assert_eq!(
        dvpn_test_support::read_node(&genesis.svm, &node).revenue,
        100
    );
    dvpn_test_support::assert_solvent(&genesis.svm, 0);
}

#[test]
fn redeem_accepts_only_voucher_signer_signature() {
    for kind in [PositionKind::Channel, PositionKind::Lease] {
        let mut genesis = dvpn_test_support::genesis();
        let node = plant_listed_node(&mut genesis);
        let owner = Keypair::new();
        let opener = Keypair::new();
        let voucher_signer_key = dvpn_test_support::voucher_signing_key(50);
        let voucher_signer = Pubkey::new_from_array(voucher_signer_key.verifying_key().to_bytes());
        let position = plant_position(
            &mut genesis,
            kind,
            &node,
            [14u8; 32],
            owner.pubkey(),
            opener.pubkey(),
            voucher_signer,
            1_000_000,
        );

        for wallet in [&owner, &opener] {
            let sig = kind.sign_with_wallet(wallet, &position, 0, 500);
            assert_redeem_fails_with(
                &mut genesis,
                kind,
                &node,
                position,
                Voucher {
                    cumulative_amount: 500,
                    signature: sig,
                },
                DvpnError::InvalidVoucherSignature,
            );
        }

        let sig = kind.sign(&voucher_signer_key, &position, 0, 500);
        let redeemer = Keypair::new();
        genesis
            .svm
            .airdrop(&redeemer.pubkey(), 10_000_000_000)
            .unwrap();
        let instruction = redeem_ix(
            &genesis,
            kind,
            &redeemer.pubkey(),
            &node,
            &[position],
            vec![Voucher {
                cumulative_amount: 500,
                signature: sig,
            }],
        );
        dvpn_test_support::send_expect_ok(&mut genesis.svm, &[instruction], &[&redeemer]);
        let redeemed = match kind {
            PositionKind::Channel => {
                dvpn_test_support::read_channel(&genesis.svm, &position).redeemed
            }
            PositionKind::Lease => dvpn_test_support::read_lease(&genesis.svm, &position).redeemed,
        };
        assert_eq!(redeemed, 500);
        dvpn_test_support::assert_solvent(&genesis.svm, 0);
    }
}

#[test]
fn open_with_small_order_voucher_signer_fails() {
    let identity = {
        let mut bytes = [0u8; 32];
        bytes[0] = 1;
        Pubkey::new_from_array(bytes)
    };
    for bad in [Pubkey::default(), identity] {
        let mut genesis = dvpn_test_support::genesis();
        let now = 1_700_000_000i64;
        dvpn_test_support::set_clock(&mut genesis.svm, now);
        let node = dvpn_test_support::plant_node(
            &mut genesis.svm,
            genesis.config,
            &dvpn_test_support::NodeSpec {
                owner: Pubkey::new_unique(),
                status: ListingStatus::Listed as u8,
                listed_at: now,
                ..Default::default()
            },
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
            1_000_000,
        );

        let channel_id = [21u8; 32];
        let open = dvpn_test_support::ix_positions::open_channel_ix(
            &genesis,
            &owner.pubkey(),
            &node,
            &owner_token,
            channel_id,
            100_000,
            PositionTerms::Voucher {
                voucher_signer: bad,
            },
            &owner.pubkey(),
        );
        let failed = dvpn_test_support::send_expect_err(&mut genesis.svm, &[open], &[&owner]);
        dvpn_test_support::assert_dvpn_error(&failed, DvpnError::InvalidVoucherSigner);
        let channel = find_channel(&dvpn::ID, &owner.pubkey(), &channel_id).0;
        assert!(dvpn_test_support::read_channel_if_exists(&genesis.svm, &channel).is_none());

        let provider = dvpn_test_support::plant_provider(
            &mut genesis.svm,
            genesis.config,
            &dvpn_test_support::ProviderSpec {
                owner: owner.pubkey(),
                provider_id: [22u8; 32],
                status: ListingStatus::Listed as u8,
                ..Default::default()
            },
        );
        let lease_id = [23u8; 32];
        let open = dvpn_test_support::ix_positions::open_lease_ix(
            &genesis,
            &provider,
            &node,
            &owner_token,
            lease_id,
            100_000,
            PositionTerms::Voucher {
                voucher_signer: bad,
            },
            &owner.pubkey(),
            &owner.pubkey(),
        );
        let failed = dvpn_test_support::send_expect_err(&mut genesis.svm, &[open], &[&owner]);
        dvpn_test_support::assert_dvpn_error(&failed, DvpnError::InvalidVoucherSigner);
        let lease = find_lease(&dvpn::ID, &provider, &lease_id).0;
        assert!(dvpn_test_support::read_lease_if_exists(&genesis.svm, &lease).is_none());

        assert_eq!(
            dvpn_test_support::read_token_amount(&genesis.svm, &genesis.vault),
            0
        );
        assert_eq!(
            dvpn_test_support::read_token_amount(&genesis.svm, &owner_token),
            1_000_000
        );
    }
}

// ─── Partial redemption: the node claims only what it served ───

fn partial_redeem_ix(
    genesis: &dvpn_test_support::Genesis,
    kind: PositionKind,
    redeemer: &Pubkey,
    node: &Pubkey,
    positions: &[Pubkey],
    vouchers: Vec<PartialVoucher>,
) -> Instruction {
    match kind {
        PositionKind::Channel => {
            dvpn_test_support::ix_positions::redeem_channel_vouchers_partial_ix(
                genesis, redeemer, node, positions, vouchers,
            )
        }
        PositionKind::Lease => dvpn_test_support::ix_positions::redeem_lease_vouchers_partial_ix(
            genesis, redeemer, node, positions, vouchers,
        ),
    }
}

fn read_redeemed(genesis: &dvpn_test_support::Genesis, kind: PositionKind, position: &Pubkey) -> u64 {
    match kind {
        PositionKind::Channel => dvpn_test_support::read_channel(&genesis.svm, position).redeemed,
        PositionKind::Lease => dvpn_test_support::read_lease(&genesis.svm, position).redeemed,
    }
}

fn funded_redeemer(genesis: &mut dvpn_test_support::Genesis) -> Keypair {
    let redeemer = Keypair::new();
    genesis
        .svm
        .airdrop(&redeemer.pubkey(), 10_000_000_000)
        .unwrap();
    redeemer
}

/// The client signed 1,000 ahead and the node served 11. A partial claim of 11 leaves 989 in
/// the escrow, and close returns it to the client. Redeeming the whole voucher returns nothing.
#[test]
fn partial_channel_redeem_refunds_the_unused_lead_at_close() {
    for (claim, refund) in [(11u64, 989u64), (1_000, 0)] {
        let mut genesis = dvpn_test_support::genesis();
        {
            let mut config_state = dvpn_test_support::read_config(&genesis.svm);
            config_state.channel_close_grace_seconds = 300;
            dvpn_test_support::plant_account(&mut genesis.svm, genesis.config, &config_state);
        }
        let node = plant_listed_node(&mut genesis);
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
        let voucher_signer_key = dvpn_test_support::voucher_signing_key(60);
        let voucher_signer = Pubkey::new_from_array(voucher_signer_key.verifying_key().to_bytes());
        let channel = dvpn_test_support::plant_channel(
            &mut genesis.svm,
            genesis.config,
            &dvpn_test_support::ChannelSpec {
                owner: owner.pubkey(),
                channel_id: [61u8; 32],
                node,
                opener: owner.pubkey(),
                voucher_signer,
                mode: PositionMode::Voucher as u8,
                status: PositionStatus::Open as u8,
                escrow: 1_000,
                rent_recipient: owner.pubkey(),
                ..Default::default()
            },
        );
        let signature =
            dvpn_test_support::sign_channel_voucher(&voucher_signer_key, &channel, 0, 1_000);

        let redeemer = funded_redeemer(&mut genesis);
        let redeem = dvpn_test_support::ix_positions::redeem_channel_vouchers_partial_ix(
            &genesis,
            &redeemer.pubkey(),
            &node,
            &[channel],
            vec![PartialVoucher {
                cumulative_amount: 1_000,
                signature,
                amount: claim,
            }],
        );
        dvpn_test_support::send_expect_ok(&mut genesis.svm, &[redeem], &[&redeemer]);
        assert_eq!(dvpn_test_support::read_channel(&genesis.svm, &channel).redeemed, claim);
        let node_state = dvpn_test_support::read_node(&genesis.svm, &node);
        assert_eq!(node_state.revenue + node_state.fees_accrued, claim);

        dvpn_test_support::set_clock(&mut genesis.svm, 10_000);
        let start = dvpn_test_support::ix_positions::start_close_channel_ix(
            &genesis,
            &channel,
            &owner.pubkey(),
        );
        dvpn_test_support::send_expect_ok(&mut genesis.svm, &[start], &[&owner]);
        dvpn_test_support::set_clock(&mut genesis.svm, 10_300);
        let close = dvpn_test_support::ix_positions::close_channel_ix(
            &genesis,
            &channel,
            &owner.pubkey(),
            None,
            &owner.pubkey(),
        );
        dvpn_test_support::send_expect_ok(&mut genesis.svm, &[close], &[&owner]);
        assert_eq!(
            dvpn_test_support::read_user(&genesis.svm, &find_user(&dvpn::ID, &owner.pubkey()).0)
                .balance,
            refund
        );
        dvpn_test_support::assert_solvent(&genesis.svm, 0);
    }
}

#[test]
fn partial_redeem_claim_above_the_signed_amount_fails() {
    for kind in [PositionKind::Channel, PositionKind::Lease] {
        let mut genesis = dvpn_test_support::genesis();
        let node = plant_listed_node(&mut genesis);
        let voucher_signer_key = dvpn_test_support::voucher_signing_key(62);
        let voucher_signer = Pubkey::new_from_array(voucher_signer_key.verifying_key().to_bytes());
        let position = plant_position(
            &mut genesis,
            kind,
            &node,
            [63u8; 32],
            Pubkey::new_unique(),
            voucher_signer,
            voucher_signer,
            1_000_000,
        );
        let signature = kind.sign(&voucher_signer_key, &position, 0, 500);
        let redeemer = funded_redeemer(&mut genesis);
        let instruction = partial_redeem_ix(
            &genesis,
            kind,
            &redeemer.pubkey(),
            &node,
            &[position],
            vec![PartialVoucher {
                cumulative_amount: 500,
                signature,
                amount: 501,
            }],
        );
        let failed =
            dvpn_test_support::send_expect_err(&mut genesis.svm, &[instruction], &[&redeemer]);
        dvpn_test_support::assert_dvpn_error(&failed, DvpnError::VoucherClaimExceedsSigned);
        assert_eq!(read_redeemed(&genesis, kind, &position), 0);
        dvpn_test_support::assert_solvent(&genesis.svm, 0);
    }
}

/// A claim can grow on the same voucher as the node serves more, never shrink, and a
/// whole-voucher redeem still takes the rest.
#[test]
fn partial_redeem_claims_grow_on_the_same_voucher() {
    for kind in [PositionKind::Channel, PositionKind::Lease] {
        let mut genesis = dvpn_test_support::genesis();
        let node = plant_listed_node(&mut genesis);
        let voucher_signer_key = dvpn_test_support::voucher_signing_key(64);
        let voucher_signer = Pubkey::new_from_array(voucher_signer_key.verifying_key().to_bytes());
        let position = plant_position(
            &mut genesis,
            kind,
            &node,
            [65u8; 32],
            Pubkey::new_unique(),
            voucher_signer,
            voucher_signer,
            1_000_000,
        );
        let signature = kind.sign(&voucher_signer_key, &position, 0, 500);
        let partial = |amount| PartialVoucher {
            cumulative_amount: 500,
            signature,
            amount,
        };
        let redeemer = funded_redeemer(&mut genesis);

        let first = partial_redeem_ix(
            &genesis,
            kind,
            &redeemer.pubkey(),
            &node,
            &[position],
            vec![partial(100)],
        );
        dvpn_test_support::send_expect_ok(&mut genesis.svm, &[first], &[&redeemer]);
        assert_eq!(read_redeemed(&genesis, kind, &position), 100);

        genesis.svm.expire_blockhash();
        let same = partial_redeem_ix(
            &genesis,
            kind,
            &redeemer.pubkey(),
            &node,
            &[position],
            vec![partial(100)],
        );
        let failed = dvpn_test_support::send_expect_err(&mut genesis.svm, &[same], &[&redeemer]);
        dvpn_test_support::assert_dvpn_error(&failed, DvpnError::RedeemableZero);
        assert_eq!(read_redeemed(&genesis, kind, &position), 100);

        genesis.svm.expire_blockhash();
        let higher = partial_redeem_ix(
            &genesis,
            kind,
            &redeemer.pubkey(),
            &node,
            &[position],
            vec![partial(250)],
        );
        dvpn_test_support::send_expect_ok(&mut genesis.svm, &[higher], &[&redeemer]);
        assert_eq!(read_redeemed(&genesis, kind, &position), 250);

        genesis.svm.expire_blockhash();
        let whole = redeem_ix(
            &genesis,
            kind,
            &redeemer.pubkey(),
            &node,
            &[position],
            vec![Voucher {
                cumulative_amount: 500,
                signature,
            }],
        );
        dvpn_test_support::send_expect_ok(&mut genesis.svm, &[whole], &[&redeemer]);
        assert_eq!(read_redeemed(&genesis, kind, &position), 500);
        let node_state = dvpn_test_support::read_node(&genesis.svm, &node);
        assert_eq!(node_state.revenue + node_state.fees_accrued, 500);
        dvpn_test_support::assert_solvent(&genesis.svm, 0);
    }
}
