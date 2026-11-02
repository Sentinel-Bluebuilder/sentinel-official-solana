use distributor_mint::create::mint_account_space;
use solana_sdk::program_pack::Pack;
use spl_token_2022_interface::extension::ExtensionType;
use spl_token_2022_interface::state::Mint;

#[test]
fn matches_extension_calculated_len_and_exceeds_base_mint_len() {
    let expected = ExtensionType::try_calculate_account_len::<Mint>(&[
        ExtensionType::ConfidentialTransferMint,
        ExtensionType::MetadataPointer,
    ])
    .unwrap();

    assert_eq!(mint_account_space(), expected);
    assert!(mint_account_space() > Mint::LEN);
}
