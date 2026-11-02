use crate::error::DvpnError;
use anchor_lang::prelude::*;

pub fn require_no_pending_unbonding(info: &UncheckedAccount) -> Result<()> {
    require!(
        *info.owner == anchor_lang::system_program::ID && info.data_is_empty(),
        DvpnError::BondPending
    );
    Ok(())
}
