use crate::error::DvpnError;
use anchor_lang::prelude::*;
use dvpn_core::constants::MAX_URI_BYTES;

pub fn write_uri(buf: &mut [u8; 128], len: &mut u8, uri: &[u8]) -> Result<()> {
    require!(uri.len() <= MAX_URI_BYTES, DvpnError::UriTooLong);
    buf.fill(0);
    buf.get_mut(..uri.len())
        .ok_or_else(|| error!(DvpnError::UriTooLong))?
        .copy_from_slice(uri);
    *len = u8::try_from(uri.len()).map_err(|_| error!(DvpnError::UriTooLong))?;
    Ok(())
}
