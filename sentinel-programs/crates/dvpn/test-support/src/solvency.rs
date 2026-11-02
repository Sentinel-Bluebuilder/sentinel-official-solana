use crate::{read_config, read_token_amount};
use anchor_lang::{AccountDeserialize, Discriminator};
use dvpn::state::{Channel, Lease, Node, NodeUnbonding, Provider, ProviderUnbonding, User};
use litesvm::LiteSVM;

#[track_caller]
pub fn assert_solvent(svm: &LiteSVM, surplus: u64) {
    let mut sum = u128::from(read_config(svm).fees_accrued);
    for (_address, acct) in svm.get_program_accounts(&dvpn::ID) {
        let data = acct.data.as_slice();
        let Some(disc) = data.get(0..8) else { continue };
        let mut rest = data;
        if disc == Node::DISCRIMINATOR {
            let node = Node::try_deserialize(&mut rest).expect("node");
            sum += u128::from(node.revenue) + u128::from(node.fees_accrued) + u128::from(node.bond);
        } else if disc == NodeUnbonding::DISCRIMINATOR {
            let node_unbonding = NodeUnbonding::try_deserialize(&mut rest).expect("node_unbonding");
            sum += u128::from(node_unbonding.bond);
        } else if disc == Provider::DISCRIMINATOR {
            let provider = Provider::try_deserialize(&mut rest).expect("provider");
            sum += u128::from(provider.revenue)
                + u128::from(provider.fees_accrued)
                + u128::from(provider.balance)
                + u128::from(provider.bond);
        } else if disc == ProviderUnbonding::DISCRIMINATOR {
            let provider_unbonding =
                ProviderUnbonding::try_deserialize(&mut rest).expect("provider_unbonding");
            sum += u128::from(provider_unbonding.bond);
        } else if disc == Channel::DISCRIMINATOR {
            let channel = Channel::try_deserialize(&mut rest).expect("channel");
            sum += u128::from(channel.escrow.saturating_sub(channel.redeemed));
        } else if disc == Lease::DISCRIMINATOR {
            let lease = Lease::try_deserialize(&mut rest).expect("lease");
            sum += u128::from(lease.escrow.saturating_sub(lease.redeemed));
        } else if disc == User::DISCRIMINATOR {
            let user = User::try_deserialize(&mut rest).expect("user");
            sum += u128::from(user.balance);
        }
    }
    let vault = read_token_amount(svm, &read_config(svm).vault);
    assert_eq!(
        u128::from(vault),
        sum + u128::from(surplus),
        "vault != ledger sum + surplus"
    );
}
