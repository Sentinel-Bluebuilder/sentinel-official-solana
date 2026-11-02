use litesvm::LiteSVM;
use solana_sdk::signer::Signer;

#[test]
fn builds_and_sets_clock() {
    let mut svm: LiteSVM = test_support::build_litesvm();
    test_support::set_clock(&mut svm, 1_700_000_000);
    let keypair = solana_sdk::signature::Keypair::new();
    svm.airdrop(&keypair.pubkey(), 1_000_000).unwrap();
}
