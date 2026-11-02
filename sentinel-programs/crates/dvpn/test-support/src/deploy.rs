use crate::PROGRAM_ID;
use litesvm::LiteSVM;
use solana_loader_v3_interface::state::UpgradeableLoaderState;
use solana_sdk::{
    account::Account, clock::Clock, pubkey::Pubkey, signature::Keypair, signer::Signer,
};
use solana_sdk_ids::bpf_loader_upgradeable;
use std::path::PathBuf;

pub fn program_so_path() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../target/deploy/dvpn.so")
}

pub fn deploy_dvpn(svm: &mut LiteSVM, authority: &Keypair) -> Pubkey {
    let elf = std::fs::read(program_so_path())
        .expect("build first: `anchor build --ignore-keys --tools-version v1.57 --arch v3`");

    svm.add_program(PROGRAM_ID, &elf)
        .expect("add_program: failed to load dvpn.so into litesvm");

    let (program_data_address, _bump) =
        Pubkey::find_program_address(&[PROGRAM_ID.as_ref()], &bpf_loader_upgradeable::id());

    let slot = svm.get_sysvar::<Clock>().slot;
    let metadata_len = UpgradeableLoaderState::size_of_programdata_metadata();
    let mut data = vec![0u8; metadata_len + elf.len()];
    bincode::serialize_into(
        &mut data[..metadata_len],
        &UpgradeableLoaderState::ProgramData {
            slot,
            upgrade_authority_address: Some(authority.pubkey()),
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
    .expect("set_account: failed to write dvpn program-data account");

    PROGRAM_ID
}

pub(crate) fn program_data_address() -> Pubkey {
    Pubkey::find_program_address(&[PROGRAM_ID.as_ref()], &bpf_loader_upgradeable::id()).0
}
