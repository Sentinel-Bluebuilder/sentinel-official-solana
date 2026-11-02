use solana_sdk::{instruction::Instruction, pubkey::Pubkey};
use solana_system_interface::instruction as system_instruction;
use spl_token_2022_interface::extension::ExtensionType;
use spl_token_2022_interface::id as token_2022_id;
use spl_token_2022_interface::instruction::{
    AuthorityType, initialize_account3, initialize_immutable_owner, mint_to, set_authority,
};
use spl_token_2022_interface::state::Account as TokenAccount;
use spl_token_metadata_interface::instruction::initialize as token_metadata_initialize;
use spl_token_metadata_interface::state::TokenMetadata;

use crate::MintError;
use crate::create::TokenMetadataParams;

#[must_use]
#[allow(clippy::expect_used)]
pub fn metadata_tlv_space(params: &TokenMetadataParams) -> usize {
    let metadata = TokenMetadata {
        name: params.name.clone(),
        symbol: params.symbol.clone(),
        uri: params.uri.clone(),
        ..Default::default()
    };
    metadata.tlv_size_of().expect("metadata size")
}

#[must_use]
#[allow(clippy::expect_used)]
pub fn token_account_space() -> usize {
    ExtensionType::try_calculate_account_len::<TokenAccount>(&[ExtensionType::ImmutableOwner])
        .expect("fixed extension set")
}

#[must_use]
pub fn initialize_metadata_ixs(
    payer: &Pubkey,
    mint: &Pubkey,
    update_authority: &Pubkey,
    mint_authority: &Pubkey,
    current_lamports: u64,
    required_lamports: u64,
    params: &TokenMetadataParams,
) -> Vec<Instruction> {
    let mut ixs = Vec::new();
    if required_lamports > current_lamports {
        #[allow(clippy::arithmetic_side_effects)]
        let top_up = required_lamports - current_lamports;
        ixs.push(system_instruction::transfer(payer, mint, top_up));
    }
    ixs.push(token_metadata_initialize(
        &token_2022_id(),
        mint,
        update_authority,
        mint,
        mint_authority,
        params.name.clone(),
        params.symbol.clone(),
        params.uri.clone(),
    ));
    ixs
}

pub fn create_token_account_ixs(
    payer: &Pubkey,
    account: &Pubkey,
    owner: &Pubkey,
    mint: &Pubkey,
    account_lamports: u64,
) -> Result<Vec<Instruction>, MintError> {
    Ok(vec![
        system_instruction::create_account(
            payer,
            account,
            account_lamports,
            token_account_space() as u64,
            &token_2022_id(),
        ),
        initialize_immutable_owner(&token_2022_id(), account)?,
        initialize_account3(&token_2022_id(), account, mint, owner)?,
    ])
}

pub fn mint_to_ix(
    mint: &Pubkey,
    destination: &Pubkey,
    mint_authority: &Pubkey,
    amount: u64,
) -> Result<Instruction, MintError> {
    Ok(mint_to(
        &token_2022_id(),
        mint,
        destination,
        mint_authority,
        &[],
        amount,
    )?)
}

pub fn seal_mint_ix(mint: &Pubkey, current_authority: &Pubkey) -> Result<Instruction, MintError> {
    Ok(set_authority(
        &token_2022_id(),
        mint,
        None,
        AuthorityType::MintTokens,
        current_authority,
        &[],
    )?)
}
