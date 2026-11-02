use crate::error::DvpnError;
use anchor_lang::prelude::*;

pub fn require_rent_recipient(
    rent_recipient: &Pubkey,
    signer: &Pubkey,
    rent_payer: &Pubkey,
) -> Result<()> {
    require!(
        *rent_recipient != Pubkey::default(),
        DvpnError::MissingRentRecipient
    );
    require!(
        rent_recipient == signer || rent_recipient == rent_payer,
        DvpnError::InvalidRentRecipient
    );
    Ok(())
}
