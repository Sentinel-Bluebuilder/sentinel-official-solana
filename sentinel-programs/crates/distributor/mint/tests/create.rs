use distributor_mint::create::{create_mint_ixs, mint_account_space};
use litesvm::LiteSVM;
use solana_sdk::{rent::Rent, signature::Keypair, signer::Signer};
use spl_token_2022_interface::extension::{
    BaseStateWithExtensions, StateWithExtensions, confidential_transfer::ConfidentialTransferMint,
    metadata_pointer::MetadataPointer,
};
use spl_token_2022_interface::state::Mint;
use txv1::TxConfig;

#[test]
fn creates_mint_with_ct_metadata_pointer_and_no_freeze() {
    let mut svm = LiteSVM::new();
    let payer = Keypair::new();
    let mint = Keypair::new();
    let authority = Keypair::new();
    svm.airdrop(&payer.pubkey(), 100_000_000_000).unwrap();

    let rent = Rent::default().minimum_balance(mint_account_space());
    let ixs = create_mint_ixs(
        &payer.pubkey(),
        &mint.pubkey(),
        &authority.pubkey(),
        &authority.pubkey(),
        rent,
    )
    .unwrap();
    let blockhash = svm.latest_blockhash();
    let transaction = txv1::build_signed(
        &payer.pubkey(),
        &ixs,
        &[&payer, &mint],
        blockhash,
        &TxConfig::new(1_400_000, 64 * 1024 * 1024),
    )
    .unwrap();
    svm.send_transaction(transaction).unwrap();

    let acct = svm.get_account(&mint.pubkey()).unwrap();
    let state = StateWithExtensions::<Mint>::unpack(&acct.data).unwrap();
    assert_eq!(state.base.decimals, distributor_core::consts::DECIMALS);

    assert!(state.base.freeze_authority.is_none());

    let metadata_pointer = state.get_extension::<MetadataPointer>().unwrap();
    assert_eq!(
        Option::<solana_sdk::pubkey::Pubkey>::from(metadata_pointer.metadata_address).unwrap(),
        mint.pubkey()
    );
    assert!(Option::<solana_sdk::pubkey::Pubkey>::from(metadata_pointer.authority).is_none());

    let confidential_transfer = state.get_extension::<ConfidentialTransferMint>().unwrap();
    assert!(bool::from(confidential_transfer.auto_approve_new_accounts));
    assert_eq!(
        confidential_transfer.auditor_elgamal_pubkey,
        Default::default()
    );
}
