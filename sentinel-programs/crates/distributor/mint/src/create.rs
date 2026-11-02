use crate::MintError;
use distributor_core::consts::DECIMALS;
use solana_sdk::{instruction::Instruction, pubkey::Pubkey};
use solana_system_interface::instruction as system_instruction;
use spl_token_2022_interface::extension::ExtensionType;
use spl_token_2022_interface::extension::confidential_transfer::instruction::initialize_mint as ct_initialize_mint;
use spl_token_2022_interface::extension::metadata_pointer::instruction::initialize as metadata_pointer_initialize;
use spl_token_2022_interface::id as token_2022_id;
use spl_token_2022_interface::instruction::initialize_mint2;
use spl_token_2022_interface::state::Mint;

pub struct TokenMetadataParams {
    pub name: String,
    pub symbol: String,
    pub uri: String,
}

#[must_use]
#[allow(clippy::expect_used)]
pub fn mint_account_space() -> usize {
    ExtensionType::try_calculate_account_len::<Mint>(&[
        ExtensionType::ConfidentialTransferMint,
        ExtensionType::MetadataPointer,
    ])
    .expect("fixed extension set")
}

pub fn create_mint_ixs(
    payer: &Pubkey,
    mint: &Pubkey,
    mint_authority: &Pubkey,
    confidential_transfer_authority: &Pubkey,
    mint_lamports: u64,
) -> Result<Vec<Instruction>, MintError> {
    let space = mint_account_space();
    Ok(vec![
        system_instruction::create_account(
            payer,
            mint,
            mint_lamports,
            space as u64,
            &token_2022_id(),
        ),
        ct_initialize_mint(
            &token_2022_id(),
            mint,
            Some(*confidential_transfer_authority),
            true,
            None,
        )?,
        metadata_pointer_initialize(&token_2022_id(), mint, None, Some(*mint))?,
        initialize_mint2(&token_2022_id(), mint, mint_authority, None, DECIMALS)?,
    ])
}
