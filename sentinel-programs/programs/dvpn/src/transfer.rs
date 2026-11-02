use anchor_lang::prelude::*;
use anchor_spl::token_interface::{
    Mint, TokenAccount, TokenInterface, TransferChecked, transfer_checked,
};
use dvpn_core::seeds::CONFIG_SEED;

#[must_use]
pub fn draw_from_pools(amount: u64, pools: &mut [u64]) -> u64 {
    let mut shortfall = amount;
    for pool in pools.iter_mut() {
        let take = shortfall.min(*pool);

        *pool = pool.saturating_sub(take);
        shortfall = shortfall.saturating_sub(take);
    }
    shortfall
}

pub fn transfer_to_vault_as_owner<'info>(
    token_program: &Interface<'info, TokenInterface>,
    source: &InterfaceAccount<'info, TokenAccount>,
    mint: &InterfaceAccount<'info, Mint>,
    vault: &InterfaceAccount<'info, TokenAccount>,
    owner: &AccountInfo<'info>,
    amount: u64,
) -> Result<()> {
    transfer_checked(
        CpiContext::new(
            token_program.key(),
            TransferChecked {
                from: source.to_account_info(),
                mint: mint.to_account_info(),
                to: vault.to_account_info(),
                authority: owner.clone(),
            },
        ),
        amount,
        mint.decimals,
    )
}

pub fn transfer_to_vault_as_delegate<'info>(
    token_program: &Interface<'info, TokenInterface>,
    source: &InterfaceAccount<'info, TokenAccount>,
    mint: &InterfaceAccount<'info, Mint>,
    vault: &InterfaceAccount<'info, TokenAccount>,
    config: &AccountInfo<'info>,
    config_bump: u8,
    amount: u64,
) -> Result<()> {
    let seeds: &[&[u8]] = &[CONFIG_SEED, &[config_bump]];
    transfer_checked(
        CpiContext::new_with_signer(
            token_program.key(),
            TransferChecked {
                from: source.to_account_info(),
                mint: mint.to_account_info(),
                to: vault.to_account_info(),
                authority: config.clone(),
            },
            &[seeds],
        ),
        amount,
        mint.decimals,
    )
}

pub fn transfer_from_vault<'info>(
    token_program: &Interface<'info, TokenInterface>,
    vault: &InterfaceAccount<'info, TokenAccount>,
    mint: &InterfaceAccount<'info, Mint>,
    destination: &InterfaceAccount<'info, TokenAccount>,
    config: &AccountInfo<'info>,
    config_bump: u8,
    amount: u64,
) -> Result<()> {
    let seeds: &[&[u8]] = &[CONFIG_SEED, &[config_bump]];
    transfer_checked(
        CpiContext::new_with_signer(
            token_program.key(),
            TransferChecked {
                from: vault.to_account_info(),
                mint: mint.to_account_info(),
                to: destination.to_account_info(),
                authority: config.clone(),
            },
            &[seeds],
        ),
        amount,
        mint.decimals,
    )
}
