use anchor_lang::{InstructionData, ToAccountMetas};
use dvpn::ID;
use dvpn_core::seeds::{find_config, find_subscription};
use solana_sdk::{instruction::Instruction, pubkey::Pubkey};

#[allow(clippy::too_many_arguments)]
pub fn open_subscription_ix_with_price(
    signer: &Pubkey,
    rent_payer: &Pubkey,
    owner: Pubkey,
    provider: Pubkey,
    plan: Pubkey,
    owner_token: Pubkey,
    config: Pubkey,
    mint: Pubkey,
    vault: Pubkey,
    price: u64,
    duration_seconds: u32,
    data_cap_gb: u32,
    max_connections: u8,
    grant: Option<Pubkey>,
    subscription_rent_recipient: Pubkey,
) -> Instruction {
    let subscription = find_subscription(&ID, &owner, &plan).0;
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::OpenSubscription {
            signer: *signer,
            rent_payer: *rent_payer,
            owner,
            config,
            plan,
            provider,
            subscription,
            grant,
            owner_token: Some(owner_token),
            vault: Some(vault),
            mint: Some(mint),
            token_program: Some(spl_token_2022_interface::id()),
            system_program: anchor_lang::solana_program::system_program::ID,
        }
        .to_account_metas(None),
        data: dvpn::instruction::OpenSubscription {
            expected_price: price,
            expected_duration_seconds: duration_seconds,
            expected_data_cap_gb: data_cap_gb,
            expected_max_connections: max_connections,
            subscription_rent_recipient,
        }
        .data(),
    }
}

#[allow(clippy::too_many_arguments)]
pub fn open_subscription_ix_without_token_accounts(
    signer: &Pubkey,
    rent_payer: &Pubkey,
    owner: Pubkey,
    provider: Pubkey,
    plan: Pubkey,
    config: Pubkey,
    price: u64,
    duration_seconds: u32,
    data_cap_gb: u32,
    max_connections: u8,
    subscription_rent_recipient: Pubkey,
) -> Instruction {
    let subscription = find_subscription(&ID, &owner, &plan).0;
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::OpenSubscription {
            signer: *signer,
            rent_payer: *rent_payer,
            owner,
            config,
            plan,
            provider,
            subscription,
            grant: None,
            owner_token: None,
            vault: None,
            mint: None,
            token_program: None,
            system_program: anchor_lang::solana_program::system_program::ID,
        }
        .to_account_metas(None),
        data: dvpn::instruction::OpenSubscription {
            expected_price: price,
            expected_duration_seconds: duration_seconds,
            expected_data_cap_gb: data_cap_gb,
            expected_max_connections: max_connections,
            subscription_rent_recipient,
        }
        .data(),
    }
}

#[allow(clippy::too_many_arguments)]
pub fn open_subscription_ix_free(
    signer: &Pubkey,
    rent_payer: &Pubkey,
    owner: Pubkey,
    provider: Pubkey,
    plan: Pubkey,
    duration_seconds: u32,
    data_cap_gb: u32,
    max_connections: u8,
    grant: Option<Pubkey>,
    subscription_rent_recipient: Pubkey,
) -> Instruction {
    let subscription = find_subscription(&ID, &owner, &plan).0;
    let (config, _bump) = find_config(&ID);
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::OpenSubscription {
            signer: *signer,
            rent_payer: *rent_payer,
            owner,
            config,
            plan,
            provider,
            subscription,
            grant,
            owner_token: None,
            vault: None,
            mint: None,
            token_program: None,
            system_program: anchor_lang::solana_program::system_program::ID,
        }
        .to_account_metas(None),
        data: dvpn::instruction::OpenSubscription {
            expected_price: 0,
            expected_duration_seconds: duration_seconds,
            expected_data_cap_gb: data_cap_gb,
            expected_max_connections: max_connections,
            subscription_rent_recipient,
        }
        .data(),
    }
}

pub fn close_subscription_ix(
    signer: &Pubkey,
    subscription: Pubkey,
    rent_recipient: Pubkey,
    grant: Option<Pubkey>,
) -> Instruction {
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::CloseSubscription {
            signer: *signer,
            subscription,
            rent_recipient,
            provider: None,
            grant,
        }
        .to_account_metas(None),
        data: dvpn::instruction::CloseSubscription {}.data(),
    }
}

pub fn close_subscription_ix_with_provider(
    signer: &Pubkey,
    subscription: Pubkey,
    provider: Pubkey,
    rent_recipient: Pubkey,
    grant: Option<Pubkey>,
) -> Instruction {
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::CloseSubscription {
            signer: *signer,
            subscription,
            rent_recipient,
            provider: Some(provider),
            grant,
        }
        .to_account_metas(None),
        data: dvpn::instruction::CloseSubscription {}.data(),
    }
}
