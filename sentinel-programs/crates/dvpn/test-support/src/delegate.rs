use litesvm::LiteSVM;
use solana_sdk::{pubkey::Pubkey, signature::Keypair, signer::Signer};

use crate::{TEST_MINT_DECIMALS, send_expect_ok};

pub fn approve_delegate(
    svm: &mut LiteSVM,
    owner: &Keypair,
    token_account: Pubkey,
    mint: Pubkey,
    config: Pubkey,
    amount: u64,
) {
    let instruction = dvpn_core::approve::approve_config_delegate_ix(
        &token_account,
        &owner.pubkey(),
        &mint,
        &config,
        amount,
        TEST_MINT_DECIMALS,
    );
    send_expect_ok(svm, std::slice::from_ref(&instruction), &[owner]);
}
