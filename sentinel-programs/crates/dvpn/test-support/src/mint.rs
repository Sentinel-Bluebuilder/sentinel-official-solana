use anchor_lang::AccountDeserialize;
use anchor_lang::solana_program::program_option::COption;
use dvpn::state::Config;
use litesvm::LiteSVM;
use solana_sdk::{account::Account, pubkey::Pubkey};
use spl_associated_token_account_interface::address::get_associated_token_address_with_program_id;
use spl_token_2022_interface::extension::immutable_owner::ImmutableOwner;
use spl_token_2022_interface::extension::transfer_fee::TransferFeeConfig;
use spl_token_2022_interface::extension::{
    BaseStateWithExtensionsMut, ExtensionType, StateWithExtensionsMut,
};
use spl_token_2022_interface::state::{Account as SplAccount, AccountState, Mint as SplMint};

pub const TEST_MINT_DECIMALS: u8 = 6;

fn build_mint_bytes(
    decimals: u8,
    mint_authority: COption<Pubkey>,
    extensions: &[ExtensionType],
) -> Vec<u8> {
    let len = ExtensionType::try_calculate_account_len::<SplMint>(extensions)
        .expect("mint extension set should size");
    let mut data = vec![0u8; len];
    let mut state = StateWithExtensionsMut::<SplMint>::unpack_uninitialized(&mut data)
        .expect("mint buffer should unpack uninitialized");
    for ext in extensions {
        match ext {
            ExtensionType::TransferFeeConfig => {
                state
                    .init_extension::<TransferFeeConfig>(false)
                    .expect("init transfer fee config extension");
            }
            other => unreachable!("mint fixture helper does not build extension {other:?}"),
        }
    }
    state.base = SplMint {
        mint_authority,
        supply: 0,
        decimals,
        is_initialized: true,
        freeze_authority: COption::None,
    };
    state.pack_base();
    state.init_account_type().expect("init mint account type");
    data
}

fn plant_mint(svm: &mut LiteSVM, data: Vec<u8>) -> Pubkey {
    let mint = Pubkey::new_unique();
    let lamports = svm.minimum_balance_for_rent_exemption(data.len());
    svm.set_account(
        mint,
        Account {
            lamports,
            data,
            owner: spl_token_2022_interface::id(),
            executable: false,
            rent_epoch: 0,
        },
    )
    .expect("set_account: plant mint");
    mint
}

pub fn plant_sealed_mint(svm: &mut LiteSVM) -> Pubkey {
    plant_mint(
        svm,
        build_mint_bytes(TEST_MINT_DECIMALS, COption::None, &[]),
    )
}

pub fn plant_unsealed_mint(svm: &mut LiteSVM) -> Pubkey {
    plant_mint(
        svm,
        build_mint_bytes(TEST_MINT_DECIMALS, COption::Some(Pubkey::new_unique()), &[]),
    )
}

pub fn plant_mint_with_forbidden_extension(svm: &mut LiteSVM) -> Pubkey {
    plant_mint(
        svm,
        build_mint_bytes(
            TEST_MINT_DECIMALS,
            COption::None,
            &[ExtensionType::TransferFeeConfig],
        ),
    )
}

fn build_token_account_bytes_with_extensions(
    mint: &Pubkey,
    owner: &Pubkey,
    amount: u64,
    immutable_owner: bool,
    reserve: &[ExtensionType],
) -> Vec<u8> {
    let mut extensions: Vec<ExtensionType> = Vec::with_capacity(reserve.len() + 1);
    if immutable_owner {
        extensions.push(ExtensionType::ImmutableOwner);
    }
    extensions.extend_from_slice(reserve);
    let len = ExtensionType::try_calculate_account_len::<SplAccount>(&extensions)
        .expect("token account extension set should size");
    let mut data = vec![0u8; len];
    let mut state = StateWithExtensionsMut::<SplAccount>::unpack_uninitialized(&mut data)
        .expect("token account buffer should unpack uninitialized");
    if immutable_owner {
        state
            .init_extension::<ImmutableOwner>(false)
            .expect("init immutable-owner extension");
    }
    state.base = SplAccount {
        mint: *mint,
        owner: *owner,
        amount,
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

fn build_token_account_bytes(
    mint: &Pubkey,
    owner: &Pubkey,
    amount: u64,
    immutable_owner: bool,
) -> Vec<u8> {
    build_token_account_bytes_with_extensions(mint, owner, amount, immutable_owner, &[])
}

fn plant_token_account(svm: &mut LiteSVM, address: Pubkey, data: Vec<u8>) -> Pubkey {
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
    .expect("set_account: plant token account");
    address
}

pub fn plant_vault(svm: &mut LiteSVM, config: Pubkey, mint: Pubkey) -> Pubkey {
    let vault = get_associated_token_address_with_program_id(
        &config,
        &mint,
        &spl_token_2022_interface::id(),
    );
    let data = build_token_account_bytes(&mint, &config, 0, true);
    plant_token_account(svm, vault, data)
}

pub fn plant_ata(svm: &mut LiteSVM, mint: Pubkey, owner: &Pubkey, amount: u64) -> Pubkey {
    let ata =
        get_associated_token_address_with_program_id(owner, &mint, &spl_token_2022_interface::id());
    let data = build_token_account_bytes(&mint, owner, amount, true);
    plant_token_account(svm, ata, data)
}

pub fn credit_vault(svm: &mut LiteSVM, config: Pubkey, amount: u64) {
    let config_account = svm.get_account(&config).expect("config account exists");
    let cfg = Config::try_deserialize(&mut config_account.data.as_slice())
        .expect("config should deserialize");
    let mut account = svm.get_account(&cfg.vault).expect("vault account exists");
    {
        let mut state = StateWithExtensionsMut::<SplAccount>::unpack(&mut account.data)
            .expect("vault should unpack");
        state.base.amount = state
            .base
            .amount
            .checked_add(amount)
            .expect("vault balance overflow");
        state.pack_base();
    }
    svm.set_account(cfg.vault, account)
        .expect("set_account: credit vault");
}
