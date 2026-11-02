use crate::PROGRAM_ID;
use anchor_lang::AccountDeserialize;
use dvpn::state::{
    Channel, Config, Grant, Lease, Node, NodeUnbonding, Plan, Provider, ProviderUnbonding,
    Subscription, User,
};
use litesvm::LiteSVM;
use solana_sdk::pubkey::Pubkey;

#[track_caller]
fn read_account<T: AccountDeserialize>(svm: &LiteSVM, address: &Pubkey) -> T {
    let account = svm.get_account(address).expect("account exists");
    T::try_deserialize(&mut account.data.as_slice()).expect("account should deserialize")
}

#[track_caller]
pub fn read_config(svm: &LiteSVM) -> Config {
    let (address, _bump) = dvpn_core::seeds::find_config(&PROGRAM_ID);
    read_account(svm, &address)
}

fn read_account_if_exists<T: AccountDeserialize>(svm: &LiteSVM, address: &Pubkey) -> Option<T> {
    let account = svm.get_account(address)?;
    T::try_deserialize(&mut account.data.as_slice()).ok()
}

macro_rules! addressed_reader {
    ($read:ident, $read_if_exists:ident, $ty:ty) => {
        #[track_caller]
        pub fn $read(svm: &LiteSVM, address: &Pubkey) -> $ty {
            read_account(svm, address)
        }
        pub fn $read_if_exists(svm: &LiteSVM, address: &Pubkey) -> Option<$ty> {
            read_account_if_exists(svm, address)
        }
    };
}

addressed_reader!(read_node, read_node_if_exists, Node);

addressed_reader!(read_channel, read_channel_if_exists, Channel);

addressed_reader!(read_lease, read_lease_if_exists, Lease);

addressed_reader!(read_user, read_user_if_exists, User);

addressed_reader!(read_grant, read_grant_if_exists, Grant);

addressed_reader!(
    read_node_unbonding,
    read_node_unbonding_if_exists,
    NodeUnbonding
);

addressed_reader!(read_provider, read_provider_if_exists, Provider);

addressed_reader!(read_subscription, read_subscription_if_exists, Subscription);

addressed_reader!(read_plan, read_plan_if_exists, Plan);

addressed_reader!(
    read_provider_unbonding,
    read_provider_unbonding_if_exists,
    ProviderUnbonding
);

pub fn is_account_closed(svm: &LiteSVM, address: &Pubkey) -> bool {
    match svm.get_account(address) {
        None => true,
        Some(account) => {
            account.lamports == 0
                && account.owner == anchor_lang::solana_program::system_program::ID
        }
    }
}

#[track_caller]
pub fn read_lamports(svm: &LiteSVM, address: &Pubkey) -> u64 {
    svm.get_account(address).expect("account exists").lamports
}
