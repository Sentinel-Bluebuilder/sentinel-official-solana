use anchor_lang::{InstructionData, ToAccountMetas};
use dvpn::ID;
use dvpn_core::args::NodePrices;
use dvpn_core::seeds::{find_config, find_node, find_node_unbonding, vault_ata_address};
use solana_sdk::{
    instruction::{AccountMeta, Instruction},
    pubkey::Pubkey,
};

#[allow(clippy::too_many_arguments)]
pub fn open_node_ix(
    owner: &Pubkey,
    rent_payer: &Pubkey,
    config: Pubkey,
    mint: Pubkey,
    vault: Pubkey,
    owner_token: Pubkey,
    node_id: [u8; 32],
    prices: NodePrices,
    protocols: u32,
    country: [u8; 2],
    uri: Vec<u8>,
    bond: u64,
) -> Instruction {
    open_node_ix_by(
        owner,
        None,
        owner,
        rent_payer,
        config,
        mint,
        vault,
        owner_token,
        node_id,
        prices,
        protocols,
        country,
        uri,
        bond,
        *rent_payer,
    )
}

#[allow(clippy::too_many_arguments)]
pub fn open_node_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    owner: &Pubkey,
    rent_payer: &Pubkey,
    config: Pubkey,
    mint: Pubkey,
    vault: Pubkey,
    owner_token: Pubkey,
    node_id: [u8; 32],
    prices: NodePrices,
    protocols: u32,
    country: [u8; 2],
    uri: Vec<u8>,
    bond: u64,
    node_rent_recipient: Pubkey,
) -> Instruction {
    let node = find_node(&ID, owner, &node_id).0;
    let node_unbonding = find_node_unbonding(&ID, owner, &node_id).0;
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::OpenNode {
            signer: *signer,
            rent_payer: *rent_payer,
            owner: *owner,
            config,
            grant,
            node,
            node_unbonding,
            owner_token,
            vault,
            mint,
            token_program: spl_token_2022_interface::id(),
            system_program: anchor_lang::solana_program::system_program::ID,
        }
        .to_account_metas(None),
        data: dvpn::instruction::OpenNode {
            node_id,
            prices,
            protocols,
            country,
            uri,
            bond,
            node_rent_recipient,
        }
        .data(),
    }
}

pub fn list_node_ix(owner: &Pubkey, node_id: [u8; 32]) -> Instruction {
    list_node_ix_by(owner, None, owner, node_id)
}

pub fn list_node_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    owner: &Pubkey,
    node_id: [u8; 32],
) -> Instruction {
    let node = find_node(&ID, owner, &node_id).0;
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::ListNode {
            signer: *signer,
            node,
            grant,
        }
        .to_account_metas(None),
        data: dvpn::instruction::ListNode {}.data(),
    }
}

pub fn list_node_ix_as_grantee(
    grantee: &Pubkey,
    owner: &Pubkey,
    grant: &Pubkey,
    node_id: [u8; 32],
) -> Instruction {
    list_node_ix_by(grantee, Some(*grant), owner, node_id)
}

pub fn delist_node_ix(owner: &Pubkey, node_id: [u8; 32]) -> Instruction {
    delist_node_ix_by(owner, None, owner, node_id)
}

pub fn delist_node_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    owner: &Pubkey,
    node_id: [u8; 32],
) -> Instruction {
    let node = find_node(&ID, owner, &node_id).0;
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::DelistNode {
            signer: *signer,
            node,
            grant,
        }
        .to_account_metas(None),
        data: dvpn::instruction::DelistNode {}.data(),
    }
}

#[allow(clippy::too_many_arguments)]
pub fn update_node_ix(
    owner: &Pubkey,
    node_id: [u8; 32],
    prices: Option<NodePrices>,
    protocols: Option<u32>,
    country: Option<[u8; 2]>,
    uri: Option<Vec<u8>>,
) -> Instruction {
    update_node_ix_by(owner, None, owner, node_id, prices, protocols, country, uri)
}

#[allow(clippy::too_many_arguments)]
pub fn update_node_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    owner: &Pubkey,
    node_id: [u8; 32],
    prices: Option<NodePrices>,
    protocols: Option<u32>,
    country: Option<[u8; 2]>,
    uri: Option<Vec<u8>>,
) -> Instruction {
    let node = find_node(&ID, owner, &node_id).0;
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::UpdateNode {
            signer: *signer,
            node,
            grant,
        }
        .to_account_metas(None),
        data: dvpn::instruction::UpdateNode {
            prices,
            protocols,
            country,
            uri,
        }
        .data(),
    }
}

pub fn sweep_node_fees_ix(
    signer: &Pubkey,
    config: Pubkey,
    vault: Pubkey,
    treasury: Pubkey,
    mint: Pubkey,
    nodes: &[Pubkey],
) -> Instruction {
    let mut accounts = dvpn::accounts::SweepNodeFees {
        signer: *signer,
        config,
        vault,
        treasury,
        mint,
        token_program: spl_token_2022_interface::id(),
    }
    .to_account_metas(None);
    for node in nodes {
        accounts.push(AccountMeta::new(*node, false));
    }
    Instruction {
        program_id: ID,
        accounts,
        data: dvpn::instruction::SweepNodeFees {}.data(),
    }
}

pub fn close_node_ix(
    owner: &Pubkey,
    rent_payer: &Pubkey,
    rent_recipient: Pubkey,
    node_id: [u8; 32],
    owner_token: Pubkey,
    mint: Pubkey,
) -> Instruction {
    close_node_ix_by(
        owner,
        None,
        owner,
        rent_payer,
        rent_recipient,
        node_id,
        owner_token,
        mint,
        *rent_payer,
    )
}

#[allow(clippy::too_many_arguments)]
pub fn close_node_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    owner: &Pubkey,
    rent_payer: &Pubkey,
    rent_recipient: Pubkey,
    node_id: [u8; 32],
    owner_token: Pubkey,
    mint: Pubkey,
    node_unbonding_rent_recipient: Pubkey,
) -> Instruction {
    let node = find_node(&ID, owner, &node_id).0;
    let node_unbonding = find_node_unbonding(&ID, owner, &node_id).0;
    let (config, _bump) = find_config(&ID);
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::CloseNode {
            signer: *signer,
            rent_payer: *rent_payer,
            config,
            grant,
            node,
            owner: *owner,
            rent_recipient,
            node_unbonding,
            owner_token,
            vault: vault_ata_address(&config, &mint),
            mint,
            token_program: spl_token_2022_interface::id(),
            system_program: anchor_lang::solana_program::system_program::ID,
        }
        .to_account_metas(None),
        data: dvpn::instruction::CloseNode {
            node_unbonding_rent_recipient,
        }
        .data(),
    }
}

pub fn withdraw_node_bond_ix(
    owner: &Pubkey,
    node_id: [u8; 32],
    owner_token: Pubkey,
    mint: Pubkey,
    rent_recipient: Pubkey,
) -> Instruction {
    withdraw_node_bond_ix_by(
        owner,
        None,
        owner,
        node_id,
        owner_token,
        mint,
        rent_recipient,
    )
}

#[allow(clippy::too_many_arguments)]
pub fn withdraw_node_bond_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    owner: &Pubkey,
    node_id: [u8; 32],
    owner_token: Pubkey,
    mint: Pubkey,
    rent_recipient: Pubkey,
) -> Instruction {
    let node_unbonding = find_node_unbonding(&ID, owner, &node_id).0;
    let (config, _bump) = find_config(&ID);
    let vault = vault_ata_address(&config, &mint);
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::WithdrawNodeBond {
            signer: *signer,
            config,
            node_unbonding,
            rent_recipient,
            owner: *owner,
            grant,
            owner_token,
            vault,
            mint,
            token_program: spl_token_2022_interface::id(),
        }
        .to_account_metas(None),
        data: dvpn::instruction::WithdrawNodeBond {}.data(),
    }
}

pub fn withdraw_node_revenue_ix(
    owner: &Pubkey,
    node_id: [u8; 32],
    owner_token: Pubkey,
    amount: u64,
    mint: Pubkey,
) -> Instruction {
    withdraw_node_revenue_ix_by(owner, None, owner, node_id, owner_token, amount, mint)
}

pub fn withdraw_node_revenue_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    owner: &Pubkey,
    node_id: [u8; 32],
    owner_token: Pubkey,
    amount: u64,
    mint: Pubkey,
) -> Instruction {
    let node = find_node(&ID, owner, &node_id).0;
    let (config, _bump) = find_config(&ID);
    let vault = vault_ata_address(&config, &mint);
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::WithdrawNodeRevenue {
            signer: *signer,
            config,
            node,
            owner: *owner,
            grant,
            owner_token,
            vault,
            mint,
            token_program: spl_token_2022_interface::id(),
        }
        .to_account_metas(None),
        data: dvpn::instruction::WithdrawNodeRevenue { amount }.data(),
    }
}

pub fn sweep_config_fees_ix(
    signer: &Pubkey,
    config: Pubkey,
    vault: Pubkey,
    treasury: Pubkey,
    mint: Pubkey,
) -> Instruction {
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::SweepConfigFees {
            signer: *signer,
            config,
            vault,
            treasury,
            mint,
            token_program: spl_token_2022_interface::id(),
        }
        .to_account_metas(None),
        data: dvpn::instruction::SweepConfigFees {}.data(),
    }
}
