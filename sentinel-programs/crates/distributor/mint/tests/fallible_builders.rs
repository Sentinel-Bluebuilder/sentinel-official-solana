use distributor_mint::MintError;
use distributor_mint::create::create_mint_ixs;
use solana_sdk::pubkey::Pubkey;

#[test]
fn create_mint_ixs_rejects_a_default_public_key_confidential_transfer_authority() {
    let payer = Pubkey::new_unique();
    let mint = Pubkey::new_unique();
    let mint_authority = Pubkey::new_unique();
    let confidential_transfer_authority = Pubkey::default();

    let result = create_mint_ixs(
        &payer,
        &mint,
        &mint_authority,
        &confidential_transfer_authority,
        0,
    );

    assert!(
        matches!(result, Err(MintError::InstructionBuildFailed(_))),
        "expected Err(MintError::InstructionBuildFailed(_)), got {result:?}"
    );
}

#[test]
fn create_mint_ixs_happy_path_returns_ok_with_four_instructions() {
    let payer = Pubkey::new_unique();
    let mint = Pubkey::new_unique();
    let mint_authority = Pubkey::new_unique();
    let confidential_transfer_authority = Pubkey::new_unique();

    let result = create_mint_ixs(
        &payer,
        &mint,
        &mint_authority,
        &confidential_transfer_authority,
        0,
    );

    assert_eq!(result.unwrap().len(), 4);
}
