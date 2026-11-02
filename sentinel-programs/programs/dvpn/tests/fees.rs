use dvpn::error::DvpnError;
use dvpn_test_support::*;
use dvpn_test_support::{ix_node, ix_provider};
use solana_sdk::pubkey::Pubkey;
use solana_sdk::signature::{Keypair, Signer};

#[test]
fn sweep_node_fees_sums_and_zeroes() {
    let genesis = genesis_paused();
    let (mut svm, cfg, mint, vault, treasury) = (
        genesis.svm,
        genesis.config,
        genesis.mint,
        genesis.vault,
        genesis.treasury,
    );
    let owner = Keypair::new().pubkey();
    let node_a = plant_node(
        &mut svm,
        cfg,
        &NodeSpec {
            owner,
            node_id: [1u8; 32],
            fees_accrued: 30,
            ..NodeSpec::default()
        },
    );
    let node_b = plant_node(
        &mut svm,
        cfg,
        &NodeSpec {
            owner,
            node_id: [2u8; 32],
            fees_accrued: 70,
            ..NodeSpec::default()
        },
    );
    let mut nodes = vec![node_a, node_b];
    nodes.sort();
    let stranger = funded_keypair(&mut svm);
    let before = read_token_amount(&svm, &treasury);
    send(
        &mut svm,
        &[ix_node::sweep_node_fees_ix(
            &stranger.pubkey(),
            cfg,
            vault,
            treasury,
            mint,
            &nodes,
        )],
        &[&stranger],
    )
    .unwrap();
    assert_eq!(read_token_amount(&svm, &treasury), before + 100);
    assert_eq!(read_node(&svm, &node_a).fees_accrued, 0);
    assert_eq!(read_node(&svm, &node_b).fees_accrued, 0);
    assert_solvent(&svm, 0);
    svm.expire_blockhash();
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_node::sweep_node_fees_ix(
                &stranger.pubkey(),
                cfg,
                vault,
                treasury,
                mint,
                &nodes,
            )],
            &[&stranger],
        ),
        DvpnError::FeesZero,
    );

    let mut rev = vec![node_a, node_b];
    rev.sort();
    rev.reverse();
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_node::sweep_node_fees_ix(
                &stranger.pubkey(),
                cfg,
                vault,
                treasury,
                mint,
                &rev,
            )],
            &[&stranger],
        ),
        DvpnError::BatchNotSorted,
    );
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_node::sweep_node_fees_ix(
                &stranger.pubkey(),
                cfg,
                vault,
                treasury,
                mint,
                &[],
            )],
            &[&stranger],
        ),
        DvpnError::BatchLengthMismatch,
    );
}

#[test]
fn sweep_node_fees_treasury_is_pinned_to_config() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault, treasury) = (
        genesis.svm,
        genesis.config,
        genesis.mint,
        genesis.vault,
        genesis.treasury,
    );
    let owner = Keypair::new().pubkey();
    let node = plant_node(
        &mut svm,
        cfg,
        &NodeSpec {
            owner,
            node_id: [3u8; 32],
            fees_accrued: 10,
            ..NodeSpec::default()
        },
    );
    let stranger = funded_keypair(&mut svm);
    let not_treasury = plant_ata(&mut svm, mint, &Pubkey::new_unique(), 0);
    assert!(
        send(
            &mut svm,
            &[ix_node::sweep_node_fees_ix(
                &stranger.pubkey(),
                cfg,
                vault,
                not_treasury,
                mint,
                &[node]
            )],
            &[&stranger],
        )
        .is_err()
    );

    send(
        &mut svm,
        &[ix_node::sweep_node_fees_ix(
            &stranger.pubkey(),
            cfg,
            vault,
            treasury,
            mint,
            &[node],
        )],
        &[&stranger],
    )
    .unwrap();
    assert_solvent(&svm, 0);
}

#[test]
fn sweep_config_fees_transfers_and_zeroes_then_refuses_on_zero() {
    let genesis = genesis();
    let (mut svm, cfg, mint, vault, treasury) = (
        genesis.svm,
        genesis.config,
        genesis.mint,
        genesis.vault,
        genesis.treasury,
    );
    let mut config_state = read_config(&svm);
    config_state.fees_accrued = 9;
    plant_account(&mut svm, cfg, &config_state);
    credit_vault(&mut svm, cfg, 9);
    assert_solvent(&svm, 0);

    let stranger = funded_keypair(&mut svm);
    let meta = send_expect_ok(
        &mut svm,
        &[ix_node::sweep_config_fees_ix(
            &stranger.pubkey(),
            cfg,
            vault,
            treasury,
            mint,
        )],
        &[&stranger],
    );
    assert_eq!(read_token_amount(&svm, &treasury), 9);
    assert_eq!(read_config(&svm).fees_accrued, 0);
    assert_event(
        &meta,
        &dvpn::events::ConfigFeesSwept {
            treasury,
            amount: 9,
        },
    );
    assert_solvent(&svm, 0);

    svm.expire_blockhash();
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_node::sweep_config_fees_ix(
                &stranger.pubkey(),
                cfg,
                vault,
                treasury,
                mint,
            )],
            &[&stranger],
        ),
        DvpnError::FeesZero,
    );
}

#[test]
fn sweep_provider_fees_sums_and_zeroes() {
    let genesis = genesis_paused();
    let (mut svm, cfg, mint, vault, treasury) = (
        genesis.svm,
        genesis.config,
        genesis.mint,
        genesis.vault,
        genesis.treasury,
    );
    let owner = Keypair::new().pubkey();
    let provider_a = plant_provider(
        &mut svm,
        cfg,
        &ProviderSpec {
            owner,
            provider_id: [1u8; 32],
            fees_accrued: 30,
            ..ProviderSpec::default()
        },
    );
    let provider_b = plant_provider(
        &mut svm,
        cfg,
        &ProviderSpec {
            owner,
            provider_id: [2u8; 32],
            fees_accrued: 70,
            ..ProviderSpec::default()
        },
    );
    let mut providers = vec![provider_a, provider_b];
    providers.sort();
    let stranger = funded_keypair(&mut svm);
    let before = read_token_amount(&svm, &treasury);
    send(
        &mut svm,
        &[ix_provider::sweep_provider_fees_ix(
            &stranger.pubkey(),
            cfg,
            vault,
            treasury,
            mint,
            &providers,
        )],
        &[&stranger],
    )
    .unwrap();
    assert_eq!(read_token_amount(&svm, &treasury), before + 100);
    assert_eq!(read_provider(&svm, &provider_a).fees_accrued, 0);
    assert_eq!(read_provider(&svm, &provider_b).fees_accrued, 0);
    assert_solvent(&svm, 0);
    svm.expire_blockhash();
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_provider::sweep_provider_fees_ix(
                &stranger.pubkey(),
                cfg,
                vault,
                treasury,
                mint,
                &providers,
            )],
            &[&stranger],
        ),
        DvpnError::FeesZero,
    );

    let mut rev = vec![provider_a, provider_b];
    rev.sort();
    rev.reverse();
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_provider::sweep_provider_fees_ix(
                &stranger.pubkey(),
                cfg,
                vault,
                treasury,
                mint,
                &rev,
            )],
            &[&stranger],
        ),
        DvpnError::BatchNotSorted,
    );
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_provider::sweep_provider_fees_ix(
                &stranger.pubkey(),
                cfg,
                vault,
                treasury,
                mint,
                &[],
            )],
            &[&stranger],
        ),
        DvpnError::BatchLengthMismatch,
    );
}
