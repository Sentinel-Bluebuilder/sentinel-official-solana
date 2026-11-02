use anchor_lang::prelude::Pubkey;
use anchor_lang::solana_program::instruction::Instruction;

#[must_use]
pub fn approve_config_delegate_ix(
    token_account: &Pubkey,
    owner: &Pubkey,
    mint: &Pubkey,
    config_pda: &Pubkey,
    amount: u64,
    decimals: u8,
) -> Instruction {
    spl_token_2022_interface::instruction::approve_checked(
        &spl_token_2022_interface::id(),
        token_account,
        mint,
        config_pda,
        owner,
        &[],
        amount,
        decimals,
    )
    .unwrap_or_else(|_| {
        Instruction::new_with_bytes(spl_token_2022_interface::id(), &[], Vec::new())
    })
}

#[cfg(test)]
mod tests {
    #![allow(clippy::unwrap_used, clippy::indexing_slicing, clippy::panic)]
    use super::*;

    #[test]
    fn builds_a_token2022_approve_checked() {
        let token_account = Pubkey::new_unique();
        let owner = Pubkey::new_unique();
        let mint = Pubkey::new_unique();
        let config = Pubkey::new_unique();
        let instruction =
            approve_config_delegate_ix(&token_account, &owner, &mint, &config, 500, 6);
        assert_eq!(instruction.program_id, spl_token_2022_interface::id());

        assert_eq!(instruction.accounts.len(), 4);
        assert_eq!(instruction.accounts[2].pubkey, config);
        assert!(instruction.accounts[3].is_signer);
    }
}
