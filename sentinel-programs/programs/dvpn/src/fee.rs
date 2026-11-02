use crate::error::DvpnError;
use anchor_lang::prelude::*;

pub fn fee_amount(gross_amount: u64, fee_bps: u16) -> Result<u64> {
    let fee = u128::from(gross_amount)
        .checked_mul(u128::from(fee_bps))
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?
        .checked_div(10_000)
        .ok_or_else(|| error!(DvpnError::ArithmeticOverflow))?;
    u64::try_from(fee).map_err(|_| error!(DvpnError::ArithmeticOverflow))
}
