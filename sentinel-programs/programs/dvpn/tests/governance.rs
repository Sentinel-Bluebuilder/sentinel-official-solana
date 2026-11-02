use anchor_lang::{InstructionData, ToAccountMetas};
use dvpn::error::DvpnError;
use dvpn_core::constants::{
    MAX_FEE_BPS, MAX_GRACE_SECONDS, MIN_GRACE_SECONDS, MIN_NODE_LISTING_TTL_SECONDS,
    MIN_UNBONDING_SECONDS, PAUSE_MASK,
};
use dvpn_test_support::ix_governance;
use dvpn_test_support::*;
use litesvm::LiteSVM;
use solana_program_option::COption;
use solana_program_pack::Pack;
use solana_sdk::account::Account;
use solana_sdk::instruction::Instruction;
use solana_sdk::pubkey::Pubkey;
use solana_sdk::signature::{Keypair, Signer};
use spl_token_2022_interface::state::{Account as SplTokenAccount, AccountState};

fn fresh_deploy() -> (LiteSVM, Keypair) {
    let mut svm = build_litesvm();
    let upgrade_authority = Keypair::new();
    svm.airdrop(&upgrade_authority.pubkey(), 1_000_000_000_000)
        .unwrap();
    deploy_dvpn(&mut svm, &upgrade_authority);
    (svm, upgrade_authority)
}

fn clone_account_to(svm: &mut LiteSVM, src: Pubkey, destination: Pubkey) {
    let account = svm
        .get_account(&src)
        .expect("clone_account_to: source account exists");
    svm.set_account(destination, account)
        .expect("clone_account_to: set_account");
}

fn vault_without_immutable_owner(svm: &mut LiteSVM, config: Pubkey, mint: Pubkey) -> Pubkey {
    let address = dvpn_core::seeds::vault_ata_address(&config, &mint);
    let account = SplTokenAccount {
        mint,
        owner: config,
        amount: 0,
        delegate: COption::None,
        state: AccountState::Initialized,
        is_native: COption::None,
        delegated_amount: 0,
        close_authority: COption::None,
    };
    let mut data = vec![0u8; SplTokenAccount::LEN];
    SplTokenAccount::pack(account, &mut data).expect("pack token account");
    let lamports = svm.minimum_balance_for_rent_exemption(data.len());
    svm.set_account(
        address,
        Account {
            lamports,
            data,
            owner: spl_token_2022_interface::id(),
            executable: false,
            rent_epoch: 0,
        },
    )
    .expect("set_account: plant vault without immutable owner");
    address
}

fn fixture() -> Fixture {
    let (mut svm, upgrade_authority) = fresh_deploy();
    let mint = plant_sealed_mint(&mut svm);
    let config = dvpn_core::seeds::find_config(&dvpn::ID).0;
    let vault = plant_vault(&mut svm, config, mint);
    let treasury = plant_ata(&mut svm, mint, &Pubkey::new_unique(), 0);
    let rent_payer = funded_keypair(&mut svm);
    Fixture {
        svm,
        upgrade_authority,
        rent_payer,
        mint,
        config,
        vault,
        treasury,
    }
}

struct Fixture {
    svm: LiteSVM,
    upgrade_authority: Keypair,
    rent_payer: Keypair,
    mint: Pubkey,
    config: Pubkey,
    vault: Pubkey,
    treasury: Pubkey,
}

#[test]
fn initialize_config_rejects_mint_with_forbidden_extension() {
    let (mut svm, upgrade_authority) = fresh_deploy();
    let mint = plant_mint_with_forbidden_extension(&mut svm);
    let config = dvpn_core::seeds::find_config(&dvpn::ID).0;
    let vault = plant_vault(&mut svm, config, mint);
    let treasury = plant_ata(&mut svm, mint, &Pubkey::new_unique(), 0);
    let rent_payer = funded_keypair(&mut svm);

    let err = send_expect_err(
        &mut svm,
        &[ix_governance::initialize_config_ix(
            &upgrade_authority.pubkey(),
            &rent_payer.pubkey(),
            config,
            mint,
            vault,
            treasury,
            default_initial_config_params(),
        )],
        &[&upgrade_authority, &rent_payer],
    );
    assert_dvpn_error(&err, DvpnError::MintExtensionForbidden);
}

#[test]
fn initialize_config_rejects_non_upgrade_authority() {
    let (mut svm, _upgrade_authority) = fresh_deploy();
    let mint = plant_sealed_mint(&mut svm);
    let config = dvpn_core::seeds::find_config(&dvpn::ID).0;
    let vault = plant_vault(&mut svm, config, mint);
    let treasury = plant_ata(&mut svm, mint, &Pubkey::new_unique(), 0);
    let stranger = funded_keypair(&mut svm);

    let err = send_expect_err(
        &mut svm,
        &[ix_governance::initialize_config_ix(
            &stranger.pubkey(),
            &stranger.pubkey(),
            config,
            mint,
            vault,
            treasury,
            default_initial_config_params(),
        )],
        &[&stranger],
    );
    assert_dvpn_error(&err, DvpnError::Unauthorized);
}

#[test]
fn initialize_config_rejects_reinitialization() {
    let fixture = fixture();
    let mut svm = fixture.svm;
    send(
        &mut svm,
        &[ix_governance::initialize_config_ix(
            &fixture.upgrade_authority.pubkey(),
            &fixture.rent_payer.pubkey(),
            fixture.config,
            fixture.mint,
            fixture.vault,
            fixture.treasury,
            default_initial_config_params(),
        )],
        &[&fixture.upgrade_authority, &fixture.rent_payer],
    )
    .unwrap();

    svm.expire_blockhash();
    let err = send_expect_err(
        &mut svm,
        &[ix_governance::initialize_config_ix(
            &fixture.upgrade_authority.pubkey(),
            &fixture.rent_payer.pubkey(),
            fixture.config,
            fixture.mint,
            fixture.vault,
            fixture.treasury,
            default_initial_config_params(),
        )],
        &[&fixture.upgrade_authority, &fixture.rent_payer],
    );
    assert!(
        matches!(
            err.err,
            solana_sdk::transaction::TransactionError::InstructionError(
                _,
                solana_sdk::instruction::InstructionError::Custom(0)
            )
        ),
        "expected Custom(0) (the system program's AccountAlreadyInUse, from init's create_account \
         hitting the already-initialized config PDA), got {:?}\n{}",
        err.err,
        err.meta.pretty_logs()
    );
}

#[test]
fn initialize_config_rejects_treasury_equal_to_vault() {
    let fixture = fixture();
    let mut svm = fixture.svm;

    let err = send_expect_err(
        &mut svm,
        &[ix_governance::initialize_config_ix(
            &fixture.upgrade_authority.pubkey(),
            &fixture.rent_payer.pubkey(),
            fixture.config,
            fixture.mint,
            fixture.vault,
            fixture.vault,
            default_initial_config_params(),
        )],
        &[&fixture.upgrade_authority, &fixture.rent_payer],
    );
    assert_dvpn_error(&err, DvpnError::InvalidTreasury);
}

#[test]
fn initialize_config_rejects_unsealed_mint() {
    let (mut svm, upgrade_authority) = fresh_deploy();
    let mint = plant_unsealed_mint(&mut svm);
    let config = dvpn_core::seeds::find_config(&dvpn::ID).0;
    let vault = plant_vault(&mut svm, config, mint);
    let treasury = plant_ata(&mut svm, mint, &Pubkey::new_unique(), 0);
    let rent_payer = funded_keypair(&mut svm);

    let err = send_expect_err(
        &mut svm,
        &[ix_governance::initialize_config_ix(
            &upgrade_authority.pubkey(),
            &rent_payer.pubkey(),
            config,
            mint,
            vault,
            treasury,
            default_initial_config_params(),
        )],
        &[&upgrade_authority, &rent_payer],
    );
    assert_dvpn_error(&err, DvpnError::MintNotSealed);
}

#[test]
fn initialize_config_rejects_vault_at_wrong_address() {
    let mut fixture = fixture();
    let wrong_address = Pubkey::new_unique();
    clone_account_to(&mut fixture.svm, fixture.vault, wrong_address);

    let err = send_expect_err(
        &mut fixture.svm,
        &[ix_governance::initialize_config_ix(
            &fixture.upgrade_authority.pubkey(),
            &fixture.rent_payer.pubkey(),
            fixture.config,
            fixture.mint,
            wrong_address,
            fixture.treasury,
            default_initial_config_params(),
        )],
        &[&fixture.upgrade_authority, &fixture.rent_payer],
    );
    assert_dvpn_error(&err, DvpnError::VaultNotAssociated);
}

#[test]
fn initialize_config_rejects_vault_without_immutable_owner() {
    let (mut svm, upgrade_authority) = fresh_deploy();
    let mint = plant_sealed_mint(&mut svm);
    let config = dvpn_core::seeds::find_config(&dvpn::ID).0;
    let vault = vault_without_immutable_owner(&mut svm, config, mint);
    let treasury = plant_ata(&mut svm, mint, &Pubkey::new_unique(), 0);
    let rent_payer = funded_keypair(&mut svm);

    let err = send_expect_err(
        &mut svm,
        &[ix_governance::initialize_config_ix(
            &upgrade_authority.pubkey(),
            &rent_payer.pubkey(),
            config,
            mint,
            vault,
            treasury,
            default_initial_config_params(),
        )],
        &[&upgrade_authority, &rent_payer],
    );
    assert_dvpn_error(&err, DvpnError::VaultNotImmutable);
}

#[test]
fn initialize_config_rejects_wrong_program_data() {
    let fixture = fixture();
    let mut svm = fixture.svm;

    let program_account = svm
        .get_account(&dvpn::ID)
        .expect("dvpn program account exists");
    let real_program_data = Pubkey::try_from(&program_account.data[4..36])
        .expect("program account carries a programdata address");
    let fake_program_data = Pubkey::new_unique();
    clone_account_to(&mut svm, real_program_data, fake_program_data);

    let instruction = Instruction {
        program_id: dvpn::ID,
        accounts: dvpn::accounts::InitializeConfig {
            authority: fixture.upgrade_authority.pubkey(),
            program: dvpn::ID,
            program_data: fake_program_data,
            config: fixture.config,
            mint: fixture.mint,
            vault: fixture.vault,
            treasury: fixture.treasury,
            token_program: spl_token_2022_interface::id(),
            rent_payer: fixture.rent_payer.pubkey(),
            system_program: anchor_lang::solana_program::system_program::ID,
        }
        .to_account_metas(None),
        data: {
            let params = default_initial_config_params();
            dvpn::instruction::InitializeConfig {
                fee_bps: params.fee_bps,
                min_node_bond: params.min_node_bond,
                min_provider_bond: params.min_provider_bond,
                unbonding_seconds: params.unbonding_seconds,
                channel_close_grace_seconds: params.channel_close_grace_seconds,
                lease_close_grace_seconds: params.lease_close_grace_seconds,
                node_listing_ttl_seconds: params.node_listing_ttl_seconds,
                paused_mask: params.paused_mask,
            }
            .data()
        },
    };
    let err = send_expect_err(
        &mut svm,
        &[instruction],
        &[&fixture.upgrade_authority, &fixture.rent_payer],
    );
    assert_dvpn_error(&err, DvpnError::Unauthorized);
}

#[test]
fn initialize_config_succeeds_paused() {
    let (mut svm, upgrade_authority) = fresh_deploy();
    let mint = plant_sealed_mint(&mut svm);
    let config = dvpn_core::seeds::find_config(&dvpn::ID).0;
    let vault = plant_vault(&mut svm, config, mint);
    let treasury = plant_ata(&mut svm, mint, &Pubkey::new_unique(), 0);
    let rent_payer = funded_keypair(&mut svm);
    let mut paused_params = default_initial_config_params();
    paused_params.paused_mask = PAUSE_MASK;

    let meta = send(
        &mut svm,
        &[ix_governance::initialize_config_ix(
            &upgrade_authority.pubkey(),
            &rent_payer.pubkey(),
            config,
            mint,
            vault,
            treasury,
            paused_params.clone(),
        )],
        &[&upgrade_authority, &rent_payer],
    )
    .unwrap();

    let config_state = read_config(&svm);
    assert_eq!(config_state.authority, upgrade_authority.pubkey());
    assert_eq!(config_state.mint, mint);
    assert_eq!(config_state.vault, vault);
    assert_eq!(config_state.treasury, treasury);
    assert_eq!(config_state.decimals, 6);
    assert_eq!(config_state.paused_mask, PAUSE_MASK);
    assert_eq!(config_state.pending_authority, Pubkey::default());
    assert_solvent(&svm, 0);
    assert_event(
        &meta,
        &dvpn::events::ConfigInitialized {
            authority: upgrade_authority.pubkey(),
            mint,
            vault,
            treasury,
            fee_bps: paused_params.fee_bps,
            min_node_bond: paused_params.min_node_bond,
            min_provider_bond: paused_params.min_provider_bond,
            unbonding_seconds: paused_params.unbonding_seconds,
            channel_close_grace_seconds: paused_params.channel_close_grace_seconds,
            lease_close_grace_seconds: paused_params.lease_close_grace_seconds,
            node_listing_ttl_seconds: paused_params.node_listing_ttl_seconds,
            paused_mask: paused_params.paused_mask,
        },
    );
}

#[test]
fn set_params_updates_subset_and_enforces_all_bounds() {
    let genesis = genesis_paused();
    let mut svm = genesis.svm;
    let authority = genesis.authority;

    send(
        &mut svm,
        &[ix_governance::set_params_ix(
            &authority.pubkey(),
            params_with_fee_bps(500),
        )],
        &[&authority],
    )
    .unwrap();
    let after_fee = read_config(&svm);
    assert_eq!(after_fee.fee_bps, 500);
    assert_eq!(after_fee.min_node_bond, 1_000);
    assert_eq!(after_fee.min_provider_bond, 1_000);
    assert_eq!(after_fee.unbonding_seconds, MIN_UNBONDING_SECONDS);
    assert_eq!(after_fee.channel_close_grace_seconds, MIN_GRACE_SECONDS);
    assert_eq!(after_fee.lease_close_grace_seconds, MIN_GRACE_SECONDS);
    assert_eq!(
        after_fee.node_listing_ttl_seconds,
        MIN_NODE_LISTING_TTL_SECONDS
    );

    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_governance::set_params_ix(
                &authority.pubkey(),
                params_with_fee_bps(MAX_FEE_BPS + 1),
            )],
            &[&authority],
        ),
        DvpnError::FeeBpsOutOfRange,
    );

    for (args, code) in [
        (
            params_with_unbonding_seconds(MIN_UNBONDING_SECONDS - 1),
            DvpnError::ParamOutOfRange,
        ),
        (
            params_with_channel_grace_seconds(MIN_GRACE_SECONDS - 1),
            DvpnError::ParamOutOfRange,
        ),
        (
            params_with_lease_grace_seconds(MAX_GRACE_SECONDS + 1),
            DvpnError::ParamOutOfRange,
        ),
        (
            params_with_node_listing_ttl_seconds(MIN_NODE_LISTING_TTL_SECONDS - 1),
            DvpnError::ParamOutOfRange,
        ),
    ] {
        assert_dvpn_error(
            &send_expect_err(
                &mut svm,
                &[ix_governance::set_params_ix(&authority.pubkey(), args)],
                &[&authority],
            ),
            code,
        );
    }

    let stranger = funded_keypair(&mut svm);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_governance::set_params_ix(
                &stranger.pubkey(),
                params_with_fee_bps(1),
            )],
            &[&stranger],
        ),
        DvpnError::Unauthorized,
    );
}

#[test]
fn set_paused_mask_and_set_treasury() {
    let genesis = genesis_paused();
    let mut svm = genesis.svm;
    let authority = genesis.authority;
    let mint = genesis.mint;
    let vault = genesis.vault;

    send(
        &mut svm,
        &[ix_governance::set_paused_mask_ix(&authority.pubkey(), 0)],
        &[&authority],
    )
    .unwrap();
    assert_eq!(read_config(&svm).paused_mask, 0);

    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_governance::set_paused_mask_ix(
                &authority.pubkey(),
                PAUSE_MASK + 1,
            )],
            &[&authority],
        ),
        DvpnError::ParamOutOfRange,
    );

    let new_treasury_owner = funded_keypair(&mut svm);
    let new_treasury = plant_ata(&mut svm, mint, &new_treasury_owner.pubkey(), 0);
    send(
        &mut svm,
        &[ix_governance::set_treasury_ix(
            &authority.pubkey(),
            new_treasury,
        )],
        &[&authority],
    )
    .unwrap();
    assert_eq!(read_config(&svm).treasury, new_treasury);

    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_governance::set_treasury_ix(&authority.pubkey(), vault)],
            &[&authority],
        ),
        DvpnError::InvalidTreasury,
    );

    let pda_owned = Pubkey::new_unique();
    clone_account_to(&mut svm, vault, pda_owned);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_governance::set_treasury_ix(
                &authority.pubkey(),
                pda_owned,
            )],
            &[&authority],
        ),
        DvpnError::InvalidTreasury,
    );
}

#[test]
fn authority_transfer_two_step() {
    let genesis = genesis_paused();
    let mut svm = genesis.svm;
    let authority = genesis.authority;
    let candidate_a = funded_keypair(&mut svm);
    let candidate_b = funded_keypair(&mut svm);

    send(
        &mut svm,
        &[ix_governance::propose_authority_ix(
            &authority.pubkey(),
            candidate_a.pubkey(),
        )],
        &[&authority],
    )
    .unwrap();
    assert_eq!(read_config(&svm).pending_authority, candidate_a.pubkey());

    send(
        &mut svm,
        &[ix_governance::propose_authority_ix(
            &authority.pubkey(),
            candidate_b.pubkey(),
        )],
        &[&authority],
    )
    .unwrap();
    assert_eq!(read_config(&svm).pending_authority, candidate_b.pubkey());

    send(
        &mut svm,
        &[ix_governance::propose_authority_ix(
            &authority.pubkey(),
            Pubkey::default(),
        )],
        &[&authority],
    )
    .unwrap();
    assert_eq!(read_config(&svm).pending_authority, Pubkey::default());

    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_governance::accept_authority_ix(&candidate_b.pubkey())],
            &[&candidate_b],
        ),
        DvpnError::MissingPendingAuthority,
    );

    let stranger = funded_keypair(&mut svm);
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_governance::propose_authority_ix(
                &stranger.pubkey(),
                candidate_a.pubkey(),
            )],
            &[&stranger],
        ),
        DvpnError::Unauthorized,
    );

    svm.expire_blockhash();
    send(
        &mut svm,
        &[ix_governance::propose_authority_ix(
            &authority.pubkey(),
            candidate_b.pubkey(),
        )],
        &[&authority],
    )
    .unwrap();
    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_governance::accept_authority_ix(&candidate_a.pubkey())],
            &[&candidate_a],
        ),
        DvpnError::Unauthorized,
    );
    send(
        &mut svm,
        &[ix_governance::accept_authority_ix(&candidate_b.pubkey())],
        &[&candidate_b],
    )
    .unwrap();

    let config_state = read_config(&svm);
    assert_eq!(config_state.authority, candidate_b.pubkey());
    assert_eq!(config_state.pending_authority, Pubkey::default());

    assert_dvpn_error(
        &send_expect_err(
            &mut svm,
            &[ix_governance::set_paused_mask_ix(&authority.pubkey(), 0)],
            &[&authority],
        ),
        DvpnError::Unauthorized,
    );
}
