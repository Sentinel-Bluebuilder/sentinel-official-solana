use anchor_lang::prelude::Pubkey;

pub const CONFIG_SEED: &[u8] = b"config";

pub const NODE_SEED: &[u8] = b"node";

pub const PROVIDER_SEED: &[u8] = b"provider";

pub const PLAN_SEED: &[u8] = b"plan";

pub const LEASE_SEED: &[u8] = b"lease";

pub const CHANNEL_SEED: &[u8] = b"channel";

pub const USER_SEED: &[u8] = b"user";

pub const SUBSCRIPTION_SEED: &[u8] = b"subscription";

pub const GRANT_SEED: &[u8] = b"grant";

pub const NODE_UNBONDING_SEED: &[u8] = b"node_unbonding";

pub const PROVIDER_UNBONDING_SEED: &[u8] = b"provider_unbonding";

#[must_use]
pub fn find_config(program_id: &Pubkey) -> (Pubkey, u8) {
    Pubkey::find_program_address(&[CONFIG_SEED], program_id)
}

#[must_use]
pub fn find_node(program_id: &Pubkey, owner: &Pubkey, node_id: &[u8; 32]) -> (Pubkey, u8) {
    Pubkey::find_program_address(&[NODE_SEED, owner.as_ref(), node_id], program_id)
}

#[must_use]
pub fn find_grant(
    program_id: &Pubkey,
    granter: &Pubkey,
    grantee: &Pubkey,
    target: &Pubkey,
) -> (Pubkey, u8) {
    Pubkey::find_program_address(
        &[
            GRANT_SEED,
            granter.as_ref(),
            grantee.as_ref(),
            target.as_ref(),
        ],
        program_id,
    )
}

#[must_use]
pub fn find_provider(program_id: &Pubkey, owner: &Pubkey, provider_id: &[u8; 32]) -> (Pubkey, u8) {
    Pubkey::find_program_address(&[PROVIDER_SEED, owner.as_ref(), provider_id], program_id)
}

#[must_use]
pub fn find_plan(program_id: &Pubkey, provider: &Pubkey, plan_id: &[u8; 32]) -> (Pubkey, u8) {
    Pubkey::find_program_address(&[PLAN_SEED, provider.as_ref(), plan_id], program_id)
}

#[must_use]
pub fn find_channel(program_id: &Pubkey, owner: &Pubkey, channel_id: &[u8; 32]) -> (Pubkey, u8) {
    Pubkey::find_program_address(&[CHANNEL_SEED, owner.as_ref(), channel_id], program_id)
}

#[must_use]
pub fn find_user(program_id: &Pubkey, owner: &Pubkey) -> (Pubkey, u8) {
    Pubkey::find_program_address(&[USER_SEED, owner.as_ref()], program_id)
}

#[must_use]
pub fn find_subscription(program_id: &Pubkey, owner: &Pubkey, plan: &Pubkey) -> (Pubkey, u8) {
    Pubkey::find_program_address(
        &[SUBSCRIPTION_SEED, owner.as_ref(), plan.as_ref()],
        program_id,
    )
}

#[must_use]
pub fn find_node_unbonding(
    program_id: &Pubkey,
    owner: &Pubkey,
    node_id: &[u8; 32],
) -> (Pubkey, u8) {
    Pubkey::find_program_address(&[NODE_UNBONDING_SEED, owner.as_ref(), node_id], program_id)
}

#[must_use]
pub fn find_provider_unbonding(
    program_id: &Pubkey,
    owner: &Pubkey,
    provider_id: &[u8; 32],
) -> (Pubkey, u8) {
    Pubkey::find_program_address(
        &[PROVIDER_UNBONDING_SEED, owner.as_ref(), provider_id],
        program_id,
    )
}

#[must_use]
pub fn vault_ata_address(config: &Pubkey, mint: &Pubkey) -> Pubkey {
    spl_associated_token_account_interface::address::get_associated_token_address_with_program_id(
        config,
        mint,
        &spl_token_2022_interface::id(),
    )
}

#[must_use]
pub fn find_lease(program_id: &Pubkey, provider: &Pubkey, lease_id: &[u8; 32]) -> (Pubkey, u8) {
    Pubkey::find_program_address(&[LEASE_SEED, provider.as_ref(), lease_id], program_id)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn grant_target_is_a_seed() {
        let program = Pubkey::new_unique();
        let granter = Pubkey::new_unique();
        let grantee = Pubkey::new_unique();
        let unscoped = find_grant(&program, &granter, &grantee, &Pubkey::default());
        let scoped = find_grant(&program, &granter, &grantee, &Pubkey::new_unique());
        assert_ne!(unscoped.0, scoped.0);
    }

    #[test]
    fn vault_ata_is_token2022_ata_of_config() {
        let config = Pubkey::new_unique();
        let mint = Pubkey::new_unique();
        let expected =
            spl_associated_token_account_interface::address::get_associated_token_address_with_program_id(
                &config, &mint, &spl_token_2022_interface::id(),
            );
        assert_eq!(vault_ata_address(&config, &mint), expected);
    }
}
