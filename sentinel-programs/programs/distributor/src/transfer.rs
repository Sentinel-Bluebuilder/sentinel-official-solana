use anchor_lang::prelude::*;
use anchor_spl::token_interface::{
    Mint, TokenAccount, TokenInterface, TransferChecked, transfer_checked,
};
use distributor_core::pda::VAULT_AUTHORITY_SEED;

pub fn vault_transfer<'info>(
    token_program: &Interface<'info, TokenInterface>,
    vault: &InterfaceAccount<'info, TokenAccount>,
    mint: &InterfaceAccount<'info, Mint>,
    destination: &InterfaceAccount<'info, TokenAccount>,
    vault_authority: &AccountInfo<'info>,
    vault_authority_bump: u8,
    amount: u64,
) -> Result<()> {
    let seeds: &[&[u8]] = &[VAULT_AUTHORITY_SEED, &[vault_authority_bump]];
    transfer_checked(
        CpiContext::new_with_signer(
            token_program.key(),
            TransferChecked {
                from: vault.to_account_info(),
                mint: mint.to_account_info(),
                to: destination.to_account_info(),
                authority: vault_authority.clone(),
            },
            &[seeds],
        ),
        amount,
        mint.decimals,
    )
}
