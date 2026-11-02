use distributor_core::pda::{
    Pubkey, find_config, find_roots, find_schedule_buffer, find_vault_authority, find_vesting_state,
};

fn dummy_program_id() -> Pubkey {
    Pubkey::new_from_array([7u8; 32])
}

#[test]
fn vesting_state_address_is_stable_and_position_dependent() {
    let program_id = dummy_program_id();
    let (address, _) = find_vesting_state(&program_id, 5, 9);
    assert_eq!(address, find_vesting_state(&program_id, 5, 9).0);
    assert_ne!(address, find_vesting_state(&program_id, 5, 10).0);
    assert_ne!(address, find_vesting_state(&program_id, 6, 9).0);
}

#[test]
fn schedule_buffer_address_depends_on_rent_payer() {
    let program_id = dummy_program_id();
    let rent_payer_one = Pubkey::new_from_array([1u8; 32]);
    let rent_payer_two = Pubkey::new_from_array([2u8; 32]);
    assert_ne!(
        find_schedule_buffer(&program_id, 5, 9, &rent_payer_one).0,
        find_schedule_buffer(&program_id, 5, 9, &rent_payer_two).0
    );
}

#[test]
fn bitmap_pda_is_shard_dependent() {
    use distributor_core::pda::find_claim_bitmap;
    let program_id = dummy_program_id();
    assert_ne!(
        find_claim_bitmap(&program_id, 0).0,
        find_claim_bitmap(&program_id, 1).0
    );
    assert_eq!(
        find_claim_bitmap(&program_id, 7).0,
        find_claim_bitmap(&program_id, 7).0
    );
}

#[test]
fn singleton_pdas_are_distinct() {
    let program_id = dummy_program_id();
    let cfg = find_config(&program_id).0;
    let roots = find_roots(&program_id).0;
    let vault = find_vault_authority(&program_id).0;
    assert_ne!(cfg, roots);
    assert_ne!(cfg, vault);
    assert_ne!(roots, vault);
}
