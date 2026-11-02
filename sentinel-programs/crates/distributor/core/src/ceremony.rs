use crate::pda::Pubkey;

pub struct CeremonyAddresses {
    pub upgrade_authority: Pubkey,
    pub payer: Pubkey,
    pub mint: Pubkey,
    pub treasury: Pubkey,
    pub vault: Pubkey,
}
