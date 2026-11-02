use anchor_lang::solana_program::program_pack::Pack;
use litesvm::{LiteSVM, types::TransactionMetadata};
use solana_sdk::{clock::Clock, pubkey, pubkey::Pubkey};
use spl_token_2022_interface::state::Account as SplAccount;

pub const SHA512_GATE: Pubkey = pubkey!("s512oDwgx8hjMnaQjXfqqrZroVj4HvC6TkN3iSSWXCh");

pub fn build_litesvm() -> LiteSVM {
    let mut feature_set = LiteSVM::mainnet_feature_set();
    feature_set.activate(&SHA512_GATE, 0);
    LiteSVM::default()
        .with_feature_set(feature_set)
        .with_builtins()
        .with_lamports(1_000_000_000_000_000)
        .with_sysvars()
        .with_feature_accounts()
        .with_default_programs()
        .with_sigverify(true)
        .with_blockhash_check(true)
}

pub fn set_clock(svm: &mut LiteSVM, unix_timestamp: i64) {
    let slot = svm.get_sysvar::<Clock>().slot;
    svm.set_sysvar(&Clock {
        unix_timestamp,
        slot,
        ..Default::default()
    });
}

#[track_caller]
pub fn read_token_amount(svm: &LiteSVM, token_account: &Pubkey) -> u64 {
    let account = svm
        .get_account(token_account)
        .expect("token account exists");
    SplAccount::unpack_from_slice(&account.data[..SplAccount::LEN])
        .expect("token account should unpack")
        .amount
}

const PROGRAM_DATA_PREFIX: &str = "Program data: ";

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}

#[track_caller]
pub fn assert_event<E: anchor_lang::Event + anchor_lang::Discriminator>(
    meta: &TransactionMetadata,
    expected: &E,
) {
    use anchor_lang::__private::base64::{Engine as _, engine::general_purpose::STANDARD};

    let expected_bytes = expected.data();
    let discriminator = E::DISCRIMINATOR;
    let mut same_discriminator: Vec<Vec<u8>> = Vec::new();
    for line in &meta.logs {
        let Some(b64) = line.strip_prefix(PROGRAM_DATA_PREFIX) else {
            continue;
        };
        let Ok(bytes) = STANDARD.decode(b64) else {
            continue;
        };
        if bytes.starts_with(discriminator) {
            same_discriminator.push(bytes);
        }
    }

    let expected_hex = hex(&expected_bytes);
    if same_discriminator.is_empty() {
        panic!(
            "no `{PROGRAM_DATA_PREFIX}` log carried event discriminator {}\n\
             expected payload: {expected_hex}\n\
             logs:\n{}",
            hex(discriminator),
            meta.logs.join("\n")
        );
    }
    let matched = same_discriminator
        .iter()
        .filter(|bytes| **bytes == expected_bytes)
        .count();
    if matched == 1 {
        return;
    }
    let got = same_discriminator
        .iter()
        .map(|bytes| hex(bytes))
        .collect::<Vec<_>>()
        .join("\n  ");
    if matched == 0 {
        panic!(
            "event {} was emitted with different bytes — a value is wrong, or the \
             event's field order/types changed under its indexers\n\
             expected: {expected_hex}\n  got: {got}",
            hex(discriminator)
        );
    }
    panic!(
        "event {} was emitted {matched} times, expected exactly once — a \
         duplicate emission double-counts under every indexer\n\
         expected: {expected_hex}\n  got: {got}",
        hex(discriminator)
    );
}
