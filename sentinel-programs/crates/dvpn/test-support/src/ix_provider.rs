use anchor_lang::{InstructionData, ToAccountMetas};
use dvpn::ID;
use dvpn_core::seeds::{find_config, find_provider, find_provider_unbonding, vault_ata_address};
use solana_sdk::{
    instruction::{AccountMeta, Instruction},
    pubkey::Pubkey,
};

#[allow(clippy::too_many_arguments)]
pub fn open_provider_ix(
    owner: &Pubkey,
    rent_payer: &Pubkey,
    config: Pubkey,
    mint: Pubkey,
    vault: Pubkey,
    owner_token: Pubkey,
    provider_id: [u8; 32],
    uri: Vec<u8>,
    bond: u64,
) -> Instruction {
    open_provider_ix_by(
        owner,
        None,
        owner,
        rent_payer,
        config,
        mint,
        vault,
        owner_token,
        provider_id,
        uri,
        bond,
        *rent_payer,
    )
}

#[allow(clippy::too_many_arguments)]
pub fn open_provider_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    owner: &Pubkey,
    rent_payer: &Pubkey,
    config: Pubkey,
    mint: Pubkey,
    vault: Pubkey,
    owner_token: Pubkey,
    provider_id: [u8; 32],
    uri: Vec<u8>,
    bond: u64,
    provider_rent_recipient: Pubkey,
) -> Instruction {
    let provider = find_provider(&ID, owner, &provider_id).0;
    let provider_unbonding = find_provider_unbonding(&ID, owner, &provider_id).0;
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::OpenProvider {
            signer: *signer,
            rent_payer: *rent_payer,
            owner: *owner,
            config,
            grant,
            provider,
            provider_unbonding,
            owner_token,
            vault,
            mint,
            token_program: spl_token_2022_interface::id(),
            system_program: anchor_lang::solana_program::system_program::ID,
        }
        .to_account_metas(None),
        data: dvpn::instruction::OpenProvider {
            provider_id,
            uri,
            bond,
            provider_rent_recipient,
        }
        .data(),
    }
}

pub fn list_provider_ix(owner: &Pubkey, provider_id: [u8; 32]) -> Instruction {
    list_provider_ix_by(owner, None, owner, provider_id)
}

pub fn list_provider_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    owner: &Pubkey,
    provider_id: [u8; 32],
) -> Instruction {
    let provider = find_provider(&ID, owner, &provider_id).0;
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::ListProvider {
            signer: *signer,
            provider,
            grant,
        }
        .to_account_metas(None),
        data: dvpn::instruction::ListProvider {}.data(),
    }
}

pub fn list_provider_ix_as_grantee(
    grantee: &Pubkey,
    owner: &Pubkey,
    grant: &Pubkey,
    provider_id: [u8; 32],
) -> Instruction {
    list_provider_ix_by(grantee, Some(*grant), owner, provider_id)
}

pub fn delist_provider_ix(owner: &Pubkey, provider_id: [u8; 32]) -> Instruction {
    delist_provider_ix_by(owner, None, owner, provider_id)
}

pub fn delist_provider_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    owner: &Pubkey,
    provider_id: [u8; 32],
) -> Instruction {
    let provider = find_provider(&ID, owner, &provider_id).0;
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::DelistProvider {
            signer: *signer,
            provider,
            grant,
        }
        .to_account_metas(None),
        data: dvpn::instruction::DelistProvider {}.data(),
    }
}

pub fn delist_provider_ix_as_grantee(
    grantee: &Pubkey,
    owner: &Pubkey,
    grant: &Pubkey,
    provider_id: [u8; 32],
) -> Instruction {
    delist_provider_ix_by(grantee, Some(*grant), owner, provider_id)
}

pub fn update_provider_ix(
    owner: &Pubkey,
    provider_id: [u8; 32],
    uri: Option<Vec<u8>>,
) -> Instruction {
    update_provider_ix_by(owner, None, owner, provider_id, uri)
}

pub fn update_provider_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    owner: &Pubkey,
    provider_id: [u8; 32],
    uri: Option<Vec<u8>>,
) -> Instruction {
    let provider = find_provider(&ID, owner, &provider_id).0;
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::UpdateProvider {
            signer: *signer,
            provider,
            grant,
        }
        .to_account_metas(None),
        data: dvpn::instruction::UpdateProvider { uri }.data(),
    }
}

pub fn update_provider_ix_as_grantee(
    grantee: &Pubkey,
    owner: &Pubkey,
    grant: &Pubkey,
    provider_id: [u8; 32],
    uri: Option<Vec<u8>>,
) -> Instruction {
    update_provider_ix_by(grantee, Some(*grant), owner, provider_id, uri)
}

pub fn withdraw_provider_revenue_ix(
    owner: &Pubkey,
    provider_id: [u8; 32],
    owner_token: Pubkey,
    amount: u64,
    mint: Pubkey,
) -> Instruction {
    withdraw_provider_revenue_ix_by(owner, None, owner, provider_id, owner_token, amount, mint)
}

pub fn withdraw_provider_revenue_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    owner: &Pubkey,
    provider_id: [u8; 32],
    owner_token: Pubkey,
    amount: u64,
    mint: Pubkey,
) -> Instruction {
    let provider = find_provider(&ID, owner, &provider_id).0;
    let (config, _bump) = find_config(&ID);
    let vault = vault_ata_address(&config, &mint);
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::WithdrawProviderRevenue {
            signer: *signer,
            config,
            provider,
            owner: *owner,
            grant,
            owner_token,
            vault,
            mint,
            token_program: spl_token_2022_interface::id(),
        }
        .to_account_metas(None),
        data: dvpn::instruction::WithdrawProviderRevenue { amount }.data(),
    }
}

pub fn withdraw_provider_balance_ix(
    owner: &Pubkey,
    provider_id: [u8; 32],
    owner_token: Pubkey,
    mint: Pubkey,
) -> Instruction {
    withdraw_provider_balance_ix_by(owner, None, owner, provider_id, owner_token, mint)
}

pub fn withdraw_provider_balance_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    owner: &Pubkey,
    provider_id: [u8; 32],
    owner_token: Pubkey,
    mint: Pubkey,
) -> Instruction {
    let provider = find_provider(&ID, owner, &provider_id).0;
    let (config, _bump) = find_config(&ID);
    let vault = vault_ata_address(&config, &mint);
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::WithdrawProviderBalance {
            signer: *signer,
            config,
            provider,
            owner: *owner,
            grant,
            owner_token,
            vault,
            mint,
            token_program: spl_token_2022_interface::id(),
        }
        .to_account_metas(None),
        data: dvpn::instruction::WithdrawProviderBalance {}.data(),
    }
}

pub fn sweep_provider_fees_ix(
    signer: &Pubkey,
    config: Pubkey,
    vault: Pubkey,
    treasury: Pubkey,
    mint: Pubkey,
    providers: &[Pubkey],
) -> Instruction {
    let mut accounts = dvpn::accounts::SweepProviderFees {
        signer: *signer,
        config,
        vault,
        treasury,
        mint,
        token_program: spl_token_2022_interface::id(),
    }
    .to_account_metas(None);
    for provider in providers {
        accounts.push(AccountMeta::new(*provider, false));
    }
    Instruction {
        program_id: ID,
        accounts,
        data: dvpn::instruction::SweepProviderFees {}.data(),
    }
}

pub fn close_provider_ix(
    owner: &Pubkey,
    rent_payer: &Pubkey,
    rent_recipient: Pubkey,
    provider_id: [u8; 32],
    owner_token: Pubkey,
    mint: Pubkey,
) -> Instruction {
    close_provider_ix_by(
        owner,
        None,
        owner,
        rent_payer,
        rent_recipient,
        provider_id,
        owner_token,
        mint,
        *rent_payer,
    )
}

#[allow(clippy::too_many_arguments)]
pub fn close_provider_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    owner: &Pubkey,
    rent_payer: &Pubkey,
    rent_recipient: Pubkey,
    provider_id: [u8; 32],
    owner_token: Pubkey,
    mint: Pubkey,
    provider_unbonding_rent_recipient: Pubkey,
) -> Instruction {
    let provider = find_provider(&ID, owner, &provider_id).0;
    let provider_unbonding = find_provider_unbonding(&ID, owner, &provider_id).0;
    let (config, _bump) = find_config(&ID);
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::CloseProvider {
            signer: *signer,
            rent_payer: *rent_payer,
            config,
            provider,
            owner: *owner,
            grant,
            rent_recipient,
            provider_unbonding,
            owner_token,
            vault: vault_ata_address(&config, &mint),
            mint,
            token_program: spl_token_2022_interface::id(),
            system_program: anchor_lang::solana_program::system_program::ID,
        }
        .to_account_metas(None),
        data: dvpn::instruction::CloseProvider {
            provider_unbonding_rent_recipient,
        }
        .data(),
    }
}

pub fn withdraw_provider_bond_ix(
    owner: &Pubkey,
    provider_id: [u8; 32],
    owner_token: Pubkey,
    mint: Pubkey,
    rent_recipient: Pubkey,
) -> Instruction {
    withdraw_provider_bond_ix_by(
        owner,
        None,
        owner,
        provider_id,
        owner_token,
        mint,
        rent_recipient,
    )
}

#[allow(clippy::too_many_arguments)]
pub fn withdraw_provider_bond_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    owner: &Pubkey,
    provider_id: [u8; 32],
    owner_token: Pubkey,
    mint: Pubkey,
    rent_recipient: Pubkey,
) -> Instruction {
    let provider_unbonding = find_provider_unbonding(&ID, owner, &provider_id).0;
    let (config, _bump) = find_config(&ID);
    let vault = vault_ata_address(&config, &mint);
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::WithdrawProviderBond {
            signer: *signer,
            config,
            provider_unbonding,
            owner: *owner,
            grant,
            rent_recipient,
            owner_token,
            vault,
            mint,
            token_program: spl_token_2022_interface::id(),
        }
        .to_account_metas(None),
        data: dvpn::instruction::WithdrawProviderBond {}.data(),
    }
}
