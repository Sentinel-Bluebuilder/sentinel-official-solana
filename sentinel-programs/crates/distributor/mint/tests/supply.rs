use distributor_mint::create::{TokenMetadataParams, create_mint_ixs, mint_account_space};
use distributor_mint::supply::{
    create_token_account_ixs, initialize_metadata_ixs, metadata_tlv_space, mint_to_ix,
    seal_mint_ix, token_account_space,
};
use litesvm::LiteSVM;
use solana_sdk::{instruction::Instruction, rent::Rent, signature::Keypair, signer::Signer};
use spl_token_2022_interface::extension::immutable_owner::ImmutableOwner;
use spl_token_2022_interface::extension::{BaseStateWithExtensions, StateWithExtensions};
use spl_token_2022_interface::state::{Account as TokenAccount, Mint};
use txv1::TxConfig;

fn params() -> TokenMetadataParams {
    TokenMetadataParams {
        name: "Sentinel".into(),
        symbol: "DVPN".into(),
        uri: "https://sentinel.co/metadata.json".into(),
    }
}

#[test]
fn full_supply_pre_mint_then_mint_authority_none() {
    let mut svm = LiteSVM::new();
    let (payer, mint, authority, treasury, vault) = (
        Keypair::new(),
        Keypair::new(),
        Keypair::new(),
        Keypair::new(),
        Keypair::new(),
    );
    svm.airdrop(&payer.pubkey(), 1_000_000_000_000).unwrap();

    let mut ixs = create_mint_ixs(
        &payer.pubkey(),
        &mint.pubkey(),
        &authority.pubkey(),
        &authority.pubkey(),
        Rent::default().minimum_balance(mint_account_space()),
    )
    .unwrap();
    let grown = mint_account_space() + metadata_tlv_space(&params());
    ixs.extend(initialize_metadata_ixs(
        &payer.pubkey(),
        &mint.pubkey(),
        &authority.pubkey(),
        &authority.pubkey(),
        Rent::default().minimum_balance(mint_account_space()),
        Rent::default().minimum_balance(grown),
        &params(),
    ));
    send(&mut svm, &[&payer, &mint, &authority], &payer, ixs);

    let ta_rent = Rent::default().minimum_balance(token_account_space());
    send(
        &mut svm,
        &[&payer, &treasury],
        &payer,
        create_token_account_ixs(
            &payer.pubkey(),
            &treasury.pubkey(),
            &payer.pubkey(),
            &mint.pubkey(),
            ta_rent,
        )
        .unwrap(),
    );
    send(
        &mut svm,
        &[&payer, &vault],
        &payer,
        create_token_account_ixs(
            &payer.pubkey(),
            &vault.pubkey(),
            &payer.pubkey(),
            &mint.pubkey(),
            ta_rent,
        )
        .unwrap(),
    );

    send(
        &mut svm,
        &[&payer, &authority],
        &payer,
        vec![
            mint_to_ix(&mint.pubkey(), &treasury.pubkey(), &authority.pubkey(), 400).unwrap(),
            mint_to_ix(&mint.pubkey(), &vault.pubkey(), &authority.pubkey(), 600).unwrap(),
            seal_mint_ix(&mint.pubkey(), &authority.pubkey()).unwrap(),
        ],
    );

    let mint_acct = svm.get_account(&mint.pubkey()).unwrap();
    let mint_state = StateWithExtensions::<Mint>::unpack(&mint_acct.data).unwrap();
    assert_eq!(mint_state.base.supply, 1000);
    assert!(
        mint_state.base.mint_authority.is_none(),
        "mint authority renounced"
    );
    let vault_acct = svm.get_account(&vault.pubkey()).unwrap();
    let vault_state = StateWithExtensions::<TokenAccount>::unpack(&vault_acct.data).unwrap();
    assert_eq!(vault_state.base.amount, 600);

    assert!(vault_state.get_extension::<ImmutableOwner>().is_ok());
}

fn send(svm: &mut LiteSVM, signers: &[&Keypair], payer: &Keypair, ixs: Vec<Instruction>) {
    let blockhash = svm.latest_blockhash();
    let transaction = txv1::build_signed(
        &payer.pubkey(),
        &ixs,
        signers,
        blockhash,
        &TxConfig::new(1_400_000, 64 * 1024 * 1024),
    )
    .unwrap();
    svm.send_transaction(transaction).unwrap();
}
