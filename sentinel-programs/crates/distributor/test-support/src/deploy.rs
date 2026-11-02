use std::path::PathBuf;

use litesvm::LiteSVM;
use solana_loader_v3_interface::state::UpgradeableLoaderState;
use solana_sdk::{
    account::Account, clock::Clock, pubkey::Pubkey, signature::Keypair, signer::Signer,
};
use solana_sdk_ids::bpf_loader_upgradeable;

pub const PROGRAM_ID: Pubkey = distributor::ID;

pub fn program_so_path() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../target/deploy/distributor.so")
}

pub fn deploy_upgradeable_distributor(svm: &mut LiteSVM, upgrade_authority: &Keypair) -> Pubkey {
    let elf = std::fs::read(program_so_path())
        .expect("build first: `anchor build --ignore-keys --tools-version v1.57 --arch v3`");

    svm.add_program(PROGRAM_ID, &elf)
        .expect("add_program: failed to load distributor.so into litesvm");

    let (program_data_address, _bump) =
        Pubkey::find_program_address(&[PROGRAM_ID.as_ref()], &bpf_loader_upgradeable::id());

    let slot = svm.get_sysvar::<Clock>().slot;
    let metadata_len = UpgradeableLoaderState::size_of_programdata_metadata();
    let mut data = vec![0u8; metadata_len + elf.len()];
    bincode::serialize_into(
        &mut data[..metadata_len],
        &UpgradeableLoaderState::ProgramData {
            slot,
            upgrade_authority_address: Some(upgrade_authority.pubkey()),
        },
    )
    .expect("UpgradeableLoaderState::ProgramData serialization should never fail");
    data[metadata_len..].copy_from_slice(&elf);

    let lamports = svm.minimum_balance_for_rent_exemption(data.len());
    svm.set_account(
        program_data_address,
        Account {
            lamports,
            data,
            owner: bpf_loader_upgradeable::id(),
            executable: false,
            rent_epoch: 0,
        },
    )
    .expect("set_account: failed to write distributor program-data account");

    PROGRAM_ID
}

pub(crate) fn find_program_data_address() -> Pubkey {
    Pubkey::find_program_address(&[PROGRAM_ID.as_ref()], &bpf_loader_upgradeable::id()).0
}

pub fn renounce_upgrade_authority(svm: &mut LiteSVM) {
    let program_data_address = find_program_data_address();
    let mut account = svm
        .get_account(&program_data_address)
        .expect("program-data account exists — deploy_upgradeable_distributor first");
    let metadata_len = UpgradeableLoaderState::size_of_programdata_metadata();
    let slot = svm.get_sysvar::<Clock>().slot;
    account.data[..metadata_len].fill(0);
    bincode::serialize_into(
        &mut account.data[..metadata_len],
        &UpgradeableLoaderState::ProgramData {
            slot,
            upgrade_authority_address: None,
        },
    )
    .expect("UpgradeableLoaderState::ProgramData serialization should never fail");
    svm.set_account(program_data_address, account)
        .expect("set_account: failed to rewrite distributor program-data account");
}
