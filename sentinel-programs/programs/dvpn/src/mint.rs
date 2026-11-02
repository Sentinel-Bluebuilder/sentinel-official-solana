use crate::error::DvpnError;
use anchor_lang::prelude::*;
use dvpn_core::constants::ALLOWED_MINT_EXTENSIONS;
use spl_token_2022_interface::extension::{BaseStateWithExtensions, StateWithExtensions};
use spl_token_2022_interface::state::Mint;

pub fn require_allowed_mint(mint_data: &[u8]) -> Result<u8> {
    let state = StateWithExtensions::<Mint>::unpack(mint_data)
        .map_err(|_| error!(DvpnError::InvalidMint))?;
    for ext in state
        .get_extension_types()
        .map_err(|_| error!(DvpnError::InvalidMint))?
    {
        require!(
            ALLOWED_MINT_EXTENSIONS.contains(&ext),
            DvpnError::MintExtensionForbidden
        );
    }
    require!(
        state.base.mint_authority.is_none() && state.base.freeze_authority.is_none(),
        DvpnError::MintNotSealed
    );
    Ok(state.base.decimals)
}

#[cfg(test)]
mod tests {
    #![allow(clippy::unwrap_used, clippy::indexing_slicing)]
    use super::*;
    use solana_program_option::COption;
    use solana_program_pack::Pack;
    use spl_token_2022_interface::extension::transfer_fee::TransferFeeConfig;
    use spl_token_2022_interface::extension::{
        BaseStateWithExtensionsMut, ExtensionType, StateWithExtensionsMut,
    };
    use spl_token_2022_interface::state::Mint;

    fn base_mint(
        decimals: u8,
        mint_authority: COption<Pubkey>,
        freeze_authority: COption<Pubkey>,
    ) -> Vec<u8> {
        let mint = Mint {
            mint_authority,
            supply: 0,
            decimals,
            is_initialized: true,
            freeze_authority,
        };
        let mut data = vec![0u8; Mint::LEN];
        Mint::pack(mint, &mut data).unwrap();
        data
    }

    fn sealed_mint_bytes(decimals: u8) -> Vec<u8> {
        base_mint(decimals, COption::None, COption::None)
    }

    fn unsealed_mint_bytes(decimals: u8) -> Vec<u8> {
        base_mint(decimals, COption::Some(Pubkey::new_unique()), COption::None)
    }

    fn forbidden_ext_mint_bytes(decimals: u8) -> Vec<u8> {
        let len =
            ExtensionType::try_calculate_account_len::<Mint>(&[ExtensionType::TransferFeeConfig])
                .unwrap();
        let mut data = vec![0u8; len];
        let mut state = StateWithExtensionsMut::<Mint>::unpack_uninitialized(&mut data).unwrap();
        state.init_extension::<TransferFeeConfig>(false).unwrap();
        state.base = Mint {
            mint_authority: COption::None,
            supply: 0,
            decimals,
            is_initialized: true,
            freeze_authority: COption::None,
        };
        state.pack_base();
        state.init_account_type().unwrap();
        data
    }

    #[test]
    fn sealed_bare_mint_ok() {
        assert_eq!(require_allowed_mint(&sealed_mint_bytes(6)).unwrap(), 6);
    }
    #[test]
    fn live_mint_authority_rejected() {
        assert!(require_allowed_mint(&unsealed_mint_bytes(6)).is_err());
    }
    #[test]
    fn forbidden_extension_rejected() {
        assert!(require_allowed_mint(&forbidden_ext_mint_bytes(6)).is_err());
    }
}
