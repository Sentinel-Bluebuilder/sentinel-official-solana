use anchor_lang::{AccountDeserialize, InstructionData, ToAccountMetas};
use dvpn_core::args::InitialConfigParams;
use dvpn_core::seeds::find_config;
use solana_instruction::Instruction;
use solana_pubkey::Pubkey;
use spl_associated_token_account::get_associated_token_address_with_program_id;
use spl_associated_token_account::instruction::create_associated_token_account_idempotent;
use spl_token_2022_interface::extension::{
    BaseStateWithExtensions, ExtensionType, StateWithExtensions,
};
use spl_token_2022_interface::state::Account as TokenAccountState;

use crate::sender::AccountReader;
use crate::steps::Step;

pub const TOKEN_2022_PROGRAM_ID: Pubkey = spl_token_2022_interface::id();

pub const STEP_DVPN_CREATE_FEE_TREASURY: &str = "dvpn_create_fee_treasury";
pub const STEP_DVPN_CREATE_VAULT: &str = "dvpn_create_vault";
pub const STEP_DVPN_INITIALIZE_CONFIG: &str = "dvpn_initialize_config";
pub const STEP_DVPN_UNPAUSE: &str = "dvpn_unpause";

#[must_use]
pub fn fee_treasury_address(fee_treasury_owner: &Pubkey, mint: &Pubkey) -> Pubkey {
    get_associated_token_address_with_program_id(fee_treasury_owner, mint, &TOKEN_2022_PROGRAM_ID)
}

#[must_use]
pub fn dvpn_vault_address(config: &Pubkey, mint: &Pubkey) -> Pubkey {
    get_associated_token_address_with_program_id(config, mint, &TOKEN_2022_PROGRAM_ID)
}

pub fn build_dvpn_create_fee_treasury_step(
    payer: &Pubkey,
    fee_treasury_owner: &Pubkey,
    mint: &Pubkey,
    config: &Pubkey,
) -> anyhow::Result<Step> {
    anyhow::ensure!(
        fee_treasury_owner != config,
        "fee treasury owner must not be the Config PDA"
    );
    anyhow::ensure!(
        fee_treasury_address(fee_treasury_owner, mint) != dvpn_vault_address(config, mint),
        "fee treasury must differ from the vault"
    );
    let instruction = create_associated_token_account_idempotent(
        payer,
        fee_treasury_owner,
        mint,
        &TOKEN_2022_PROGRAM_ID,
    );
    Ok(Step {
        name: STEP_DVPN_CREATE_FEE_TREASURY.into(),
        ixs: vec![instruction],
        extra_signers: vec![],
        resume: None,
    })
}

#[must_use]
pub fn build_dvpn_create_vault_step(payer: &Pubkey, config: &Pubkey, mint: &Pubkey) -> Step {
    let instruction =
        create_associated_token_account_idempotent(payer, config, mint, &TOKEN_2022_PROGRAM_ID);
    Step {
        name: STEP_DVPN_CREATE_VAULT.into(),
        ixs: vec![instruction],
        extra_signers: vec![],
        resume: None,
    }
}

pub fn check_fee_treasury(
    data: &[u8],
    fee_treasury_owner: &Pubkey,
    mint: &Pubkey,
) -> anyhow::Result<()> {
    let state = StateWithExtensions::<TokenAccountState>::unpack(data)
        .map_err(|error| anyhow::anyhow!("treasury unpack failed: {error}"))?;
    anyhow::ensure!(
        state.base.owner == *fee_treasury_owner,
        "treasury owner is not the fee_treasury_owner"
    );
    anyhow::ensure!(state.base.mint == *mint, "treasury mint mismatch");
    let exts = state
        .get_extension_types()
        .map_err(|error| anyhow::anyhow!("{error}"))?;
    anyhow::ensure!(
        !exts.contains(&ExtensionType::MemoTransfer),
        "treasury requires a memo; sweeps issue none"
    );
    anyhow::ensure!(
        !exts.contains(&ExtensionType::ConfidentialTransferAccount),
        "treasury refuses plaintext credits"
    );
    Ok(())
}

#[must_use]
pub fn default_initial_config_params() -> InitialConfigParams {
    InitialConfigParams {
        fee_bps: 200,
        min_node_bond: 1_000_000_000,
        min_provider_bond: 10_000_000_000,
        unbonding_seconds: 604_800,
        channel_close_grace_seconds: 86_400,
        lease_close_grace_seconds: 86_400,
        node_listing_ttl_seconds: 86_400,
        paused_mask: dvpn_core::constants::PAUSE_MASK,
    }
}

#[allow(clippy::too_many_arguments)]
#[must_use]
pub fn build_dvpn_initialize_config_step(
    upgrade_authority: &Pubkey,
    program_id: &Pubkey,
    config: &Pubkey,
    mint: &Pubkey,
    vault: &Pubkey,
    treasury: &Pubkey,
    rent_payer: &Pubkey,
    params: &InitialConfigParams,
) -> Step {
    let program_data = solana_loader_v3_interface::get_program_data_address(program_id);
    let instruction = Instruction {
        program_id: *program_id,
        accounts: dvpn::accounts::InitializeConfig {
            authority: *upgrade_authority,
            program: *program_id,
            program_data,
            config: *config,
            mint: *mint,
            vault: *vault,
            treasury: *treasury,
            token_program: TOKEN_2022_PROGRAM_ID,
            rent_payer: *rent_payer,
            system_program: anchor_lang::solana_program::system_program::ID,
        }
        .to_account_metas(None),
        data: dvpn::instruction::InitializeConfig {
            fee_bps: params.fee_bps,
            min_node_bond: params.min_node_bond,
            min_provider_bond: params.min_provider_bond,
            unbonding_seconds: params.unbonding_seconds,
            channel_close_grace_seconds: params.channel_close_grace_seconds,
            lease_close_grace_seconds: params.lease_close_grace_seconds,
            node_listing_ttl_seconds: params.node_listing_ttl_seconds,
            paused_mask: params.paused_mask,
        }
        .data(),
    };
    Step {
        name: STEP_DVPN_INITIALIZE_CONFIG.into(),
        ixs: vec![instruction],
        extra_signers: vec![],
        resume: None,
    }
}

#[must_use]
pub fn has_dvpn_config(reader: &dyn AccountReader, config: &Pubkey) -> bool {
    reader.account_exists(config)
}

#[must_use]
pub fn build_dvpn_unpause_step(program_id: &Pubkey, upgrade_authority: &Pubkey) -> Step {
    let (config, _) = find_config(program_id);
    let instruction = Instruction {
        program_id: *program_id,
        accounts: dvpn::accounts::SetPausedMask {
            authority: *upgrade_authority,
            config,
        }
        .to_account_metas(None),
        data: dvpn::instruction::SetPausedMask { paused_mask: 0 }.data(),
    };
    Step {
        name: STEP_DVPN_UNPAUSE.into(),
        ixs: vec![instruction],
        extra_signers: vec![],
        resume: None,
    }
}

#[must_use]
pub fn is_dvpn_config_unpaused(reader: &dyn AccountReader, config: &Pubkey) -> bool {
    let Some(data) = reader.account_data(config) else {
        return false;
    };
    let mut slice: &[u8] = &data;
    dvpn::state::Config::try_deserialize(&mut slice)
        .map(|config| config.paused_mask == 0)
        .unwrap_or(false)
}

#[cfg(test)]
#[allow(clippy::expect_used)]
fn fabricate_token_account(owner: &Pubkey, mint: &Pubkey, exts: &[ExtensionType]) -> Vec<u8> {
    use anchor_lang::solana_program::program_option::COption;
    use spl_token_2022_interface::extension::confidential_transfer::ConfidentialTransferAccount;
    use spl_token_2022_interface::extension::memo_transfer::MemoTransfer;
    use spl_token_2022_interface::extension::{BaseStateWithExtensionsMut, StateWithExtensionsMut};
    use spl_token_2022_interface::state::AccountState;

    let len = ExtensionType::try_calculate_account_len::<TokenAccountState>(exts)
        .expect("fixture extension set should size");
    let mut data = vec![0u8; len];
    let mut state = StateWithExtensionsMut::<TokenAccountState>::unpack_uninitialized(&mut data)
        .expect("fixture token account buffer should unpack uninitialized");
    for ext in exts {
        match ext {
            ExtensionType::MemoTransfer => {
                state
                    .init_extension::<MemoTransfer>(false)
                    .expect("init memo-transfer extension");
            }
            ExtensionType::ConfidentialTransferAccount => {
                state
                    .init_extension::<ConfidentialTransferAccount>(false)
                    .expect("init confidential-transfer-account extension");
            }
            other => unreachable!("fabricate_token_account does not build extension {other:?}"),
        }
    }
    state.base = TokenAccountState {
        mint: *mint,
        owner: *owner,
        amount: 0,
        delegate: COption::None,
        state: AccountState::Initialized,
        is_native: COption::None,
        delegated_amount: 0,
        close_authority: COption::None,
    };
    state.pack_base();
    state.init_account_type().expect("init token account type");
    data
}

#[cfg(test)]
#[allow(clippy::unwrap_used, clippy::expect_used, clippy::panic)]
mod tests {
    use super::*;

    #[test]
    fn build_dvpn_create_fee_treasury_step_rejects_config_as_owner() {
        let payer = Pubkey::new_unique();
        let config = Pubkey::new_unique();
        let mint = Pubkey::new_unique();
        assert!(build_dvpn_create_fee_treasury_step(&payer, &config, &mint, &config).is_err());
    }

    #[test]
    fn check_fee_treasury_rejects_confidential_transfer() {
        let fee_treasury_owner = Pubkey::new_unique();
        let mint = Pubkey::new_unique();
        let data = fabricate_token_account(
            &fee_treasury_owner,
            &mint,
            &[ExtensionType::ConfidentialTransferAccount],
        );
        assert!(check_fee_treasury(&data, &fee_treasury_owner, &mint).is_err());
    }

    #[test]
    fn build_dvpn_unpause_step_targets_set_paused_mask_zero_with_upgrade_authority_signing() {
        let program_id = Pubkey::new_unique();
        let upgrade_authority = Pubkey::new_unique();
        let step = build_dvpn_unpause_step(&program_id, &upgrade_authority);
        assert_eq!(step.name, STEP_DVPN_UNPAUSE);
        assert!(step.ixs.iter().any(|instruction| {
            instruction
                .accounts
                .iter()
                .any(|account| account.pubkey == upgrade_authority && account.is_signer)
        }));
    }

    struct FakeAccountReader(Option<Vec<u8>>);
    impl AccountReader for FakeAccountReader {
        fn account_exists(&self, _address: &Pubkey) -> bool {
            self.0.is_some()
        }
        fn account_data(&self, _address: &Pubkey) -> Option<Vec<u8>> {
            self.0.clone()
        }
    }

    fn fabricate_config_paused(paused_mask: u8) -> Vec<u8> {
        let cfg = dvpn::state::Config {
            authority: Pubkey::new_unique(),
            pending_authority: Pubkey::default(),
            mint: Pubkey::new_unique(),
            decimals: 6,
            vault: Pubkey::new_unique(),
            treasury: Pubkey::new_unique(),
            fee_bps: 200,
            min_node_bond: 0,
            min_provider_bond: 0,
            unbonding_seconds: 0,
            channel_close_grace_seconds: 0,
            lease_close_grace_seconds: 0,
            node_listing_ttl_seconds: 0,
            paused_mask,
            bump: 0,
            fees_accrued: 0,
            reserved: [0u8; 64],
        };
        let mut data = Vec::new();
        anchor_lang::AccountSerialize::try_serialize(&cfg, &mut data).expect("serialize fixture");
        data
    }

    #[test]
    fn is_dvpn_config_unpaused_reads_the_paused_byte() {
        let config = Pubkey::new_unique();
        assert!(!is_dvpn_config_unpaused(
            &FakeAccountReader(Some(fabricate_config_paused(0x3F))),
            &config
        ));
        assert!(is_dvpn_config_unpaused(
            &FakeAccountReader(Some(fabricate_config_paused(0))),
            &config
        ));
    }
}
