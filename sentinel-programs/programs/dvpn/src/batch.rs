use crate::error::DvpnError;
use anchor_lang::prelude::*;

pub fn require_sorted_unique(accounts: &[AccountInfo]) -> Result<()> {
    for pair in accounts.windows(2) {
        if let (Some(first), Some(second)) = (pair.first(), pair.get(1)) {
            require!(
                first.key().to_bytes() < second.key().to_bytes(),
                DvpnError::BatchNotSorted
            );
        }
    }
    Ok(())
}

pub fn require_same_nonempty_len(accounts_len: usize, args_len: usize) -> Result<()> {
    require!(
        accounts_len > 0 && accounts_len == args_len,
        DvpnError::BatchLengthMismatch
    );
    Ok(())
}

#[cfg(test)]
mod tests {
    #![allow(clippy::unwrap_used)]
    use super::*;

    #[test]
    fn len_must_match_and_be_nonzero() {
        assert!(require_same_nonempty_len(3, 3).is_ok());
        assert!(require_same_nonempty_len(0, 0).is_err());
        assert!(require_same_nonempty_len(3, 2).is_err());
    }

    #[test]
    fn sorted_unique_guard() {
        let mut first_lamports = 0u64;
        let mut first_data: [u8; 0] = [];
        let first_key = Pubkey::new_from_array([1u8; 32]);
        let mut second_lamports = 0u64;
        let mut second_data: [u8; 0] = [];
        let second_key = Pubkey::new_from_array([2u8; 32]);
        let owner = Pubkey::default();
        let first = AccountInfo::new(
            &first_key,
            false,
            false,
            &mut first_lamports,
            &mut first_data,
            &owner,
            false,
        );
        let second = AccountInfo::new(
            &second_key,
            false,
            false,
            &mut second_lamports,
            &mut second_data,
            &owner,
            false,
        );
        assert!(require_sorted_unique(&[first.clone(), second.clone()]).is_ok());
        assert!(require_sorted_unique(&[second, first]).is_err());
    }
}
