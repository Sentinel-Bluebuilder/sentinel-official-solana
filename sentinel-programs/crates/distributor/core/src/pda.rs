pub use solana_pubkey::Pubkey;

pub const CONFIG_SEED: &[u8] = b"config";
pub const ROOTS_SEED: &[u8] = b"roots";
pub const VAULT_AUTHORITY_SEED: &[u8] = b"vault_authority";
pub const VESTING_STATE_SEED: &[u8] = b"vesting_state";
pub const SCHEDULE_BUFFER_SEED: &[u8] = b"schedule_buffer";
pub const CLAIM_BITMAP_SEED: &[u8] = b"claim_bitmap";

#[must_use]
pub fn claim_bitmap_seeds(shard_id: u16) -> ([u8; 12], [u8; 2]) {
    let mut tag = [0u8; 12];
    tag.copy_from_slice(CLAIM_BITMAP_SEED);
    (tag, shard_id.to_le_bytes())
}

#[must_use]
pub fn vesting_state_seeds(shard_id: u16, leaf_index: u32) -> ([u8; 13], [u8; 2], [u8; 4]) {
    let mut tag = [0u8; 13];
    tag.copy_from_slice(VESTING_STATE_SEED);
    (tag, shard_id.to_le_bytes(), leaf_index.to_le_bytes())
}

#[must_use]
pub fn schedule_buffer_seeds(
    shard_id: u16,
    leaf_index: u32,
    rent_payer: &Pubkey,
) -> ([u8; 15], [u8; 2], [u8; 4], [u8; 32]) {
    let mut tag = [0u8; 15];
    tag.copy_from_slice(SCHEDULE_BUFFER_SEED);
    (
        tag,
        shard_id.to_le_bytes(),
        leaf_index.to_le_bytes(),
        rent_payer.to_bytes(),
    )
}

#[must_use]
pub fn find_config(program_id: &Pubkey) -> (Pubkey, u8) {
    Pubkey::find_program_address(&[CONFIG_SEED], program_id)
}

#[must_use]
pub fn find_roots(program_id: &Pubkey) -> (Pubkey, u8) {
    Pubkey::find_program_address(&[ROOTS_SEED], program_id)
}

#[must_use]
pub fn find_vault_authority(program_id: &Pubkey) -> (Pubkey, u8) {
    Pubkey::find_program_address(&[VAULT_AUTHORITY_SEED], program_id)
}

#[must_use]
pub fn find_claim_bitmap(program_id: &Pubkey, shard_id: u16) -> (Pubkey, u8) {
    let (tag, shard) = claim_bitmap_seeds(shard_id);
    Pubkey::find_program_address(&[&tag, &shard], program_id)
}

#[must_use]
pub fn find_vesting_state(program_id: &Pubkey, shard_id: u16, leaf_index: u32) -> (Pubkey, u8) {
    let (tag, shard, index) = vesting_state_seeds(shard_id, leaf_index);
    Pubkey::find_program_address(&[&tag, &shard, &index], program_id)
}

#[must_use]
pub fn find_schedule_buffer(
    program_id: &Pubkey,
    shard_id: u16,
    leaf_index: u32,
    rent_payer: &Pubkey,
) -> (Pubkey, u8) {
    let (tag, shard, index, rent_payer_bytes) =
        schedule_buffer_seeds(shard_id, leaf_index, rent_payer);
    Pubkey::find_program_address(&[&tag, &shard, &index, &rent_payer_bytes], program_id)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn seed_strings_equal_state_names() {
        assert_eq!(VAULT_AUTHORITY_SEED, b"vault_authority");
        assert_eq!(VESTING_STATE_SEED, b"vesting_state");
        assert_eq!(SCHEDULE_BUFFER_SEED, b"schedule_buffer");
        assert_eq!(CLAIM_BITMAP_SEED, b"claim_bitmap");
        assert_eq!(CONFIG_SEED, b"config");
        assert_eq!(ROOTS_SEED, b"roots");
    }
}
