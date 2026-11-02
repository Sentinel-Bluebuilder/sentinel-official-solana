use anchor_lang::{InstructionData, ToAccountMetas};
use dvpn::ID;
use dvpn_core::seeds::{find_config, find_plan};
use solana_sdk::{instruction::Instruction, pubkey::Pubkey};

#[allow(clippy::too_many_arguments)]
pub fn open_plan_ix(
    owner: &Pubkey,
    provider: Pubkey,
    plan_id: [u8; 32],
    price: u64,
    duration_seconds: u32,
    data_cap_gb: u32,
    max_connections: u8,
) -> Instruction {
    open_plan_ix_by(
        owner,
        None,
        owner,
        provider,
        plan_id,
        price,
        duration_seconds,
        data_cap_gb,
        max_connections,
        *owner,
    )
}

#[allow(clippy::too_many_arguments)]
pub fn open_plan_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    rent_payer: &Pubkey,
    provider: Pubkey,
    plan_id: [u8; 32],
    price: u64,
    duration_seconds: u32,
    data_cap_gb: u32,
    max_connections: u8,
    plan_rent_recipient: Pubkey,
) -> Instruction {
    let (config, _bump) = find_config(&ID);
    let plan = find_plan(&ID, &provider, &plan_id).0;
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::OpenPlan {
            signer: *signer,
            rent_payer: *rent_payer,
            config,
            provider,
            grant,
            plan,
            system_program: anchor_lang::solana_program::system_program::ID,
        }
        .to_account_metas(None),
        data: dvpn::instruction::OpenPlan {
            plan_id,
            price,
            duration_seconds,
            data_cap_gb,
            max_connections,
            plan_rent_recipient,
        }
        .data(),
    }
}

#[allow(clippy::too_many_arguments)]
pub fn open_plan_ix_as_grantee(
    grantee: &Pubkey,
    grant: &Pubkey,
    provider: Pubkey,
    plan_id: [u8; 32],
    price: u64,
    duration_seconds: u32,
    data_cap_gb: u32,
    max_connections: u8,
) -> Instruction {
    open_plan_ix_by(
        grantee,
        Some(*grant),
        grantee,
        provider,
        plan_id,
        price,
        duration_seconds,
        data_cap_gb,
        max_connections,
        *grantee,
    )
}

pub fn list_plan_ix(owner: &Pubkey, provider: Pubkey, plan_id: [u8; 32]) -> Instruction {
    let plan = find_plan(&ID, &provider, &plan_id).0;
    list_plan_ix_by(owner, None, provider, plan)
}

pub fn list_plan_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    provider: Pubkey,
    plan: Pubkey,
) -> Instruction {
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::ListPlan {
            signer: *signer,
            provider,
            grant,
            plan,
        }
        .to_account_metas(None),
        data: dvpn::instruction::ListPlan {}.data(),
    }
}

pub fn list_plan_ix_as_grantee(
    grantee: &Pubkey,
    grant: &Pubkey,
    provider: Pubkey,
    plan_id: [u8; 32],
) -> Instruction {
    let plan = find_plan(&ID, &provider, &plan_id).0;
    list_plan_ix_by(grantee, Some(*grant), provider, plan)
}

pub fn delist_plan_ix(owner: &Pubkey, provider: Pubkey, plan_id: [u8; 32]) -> Instruction {
    let plan = find_plan(&ID, &provider, &plan_id).0;
    delist_plan_ix_by(owner, None, provider, plan)
}

pub fn delist_plan_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    provider: Pubkey,
    plan: Pubkey,
) -> Instruction {
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::DelistPlan {
            signer: *signer,
            provider,
            grant,
            plan,
        }
        .to_account_metas(None),
        data: dvpn::instruction::DelistPlan {}.data(),
    }
}

pub fn delist_plan_ix_as_grantee(
    grantee: &Pubkey,
    grant: &Pubkey,
    provider: Pubkey,
    plan_id: [u8; 32],
) -> Instruction {
    let plan = find_plan(&ID, &provider, &plan_id).0;
    delist_plan_ix_by(grantee, Some(*grant), provider, plan)
}

#[allow(clippy::too_many_arguments)]
pub fn update_plan_ix(
    owner: &Pubkey,
    provider: Pubkey,
    plan_id: [u8; 32],
    price: Option<u64>,
    duration_seconds: Option<u32>,
    data_cap_gb: Option<u32>,
    max_connections: Option<u8>,
) -> Instruction {
    let plan = find_plan(&ID, &provider, &plan_id).0;
    update_plan_ix_by(
        owner,
        None,
        provider,
        plan,
        price,
        duration_seconds,
        data_cap_gb,
        max_connections,
    )
}

#[allow(clippy::too_many_arguments)]
pub fn update_plan_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    provider: Pubkey,
    plan: Pubkey,
    price: Option<u64>,
    duration_seconds: Option<u32>,
    data_cap_gb: Option<u32>,
    max_connections: Option<u8>,
) -> Instruction {
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::UpdatePlan {
            signer: *signer,
            provider,
            grant,
            plan,
        }
        .to_account_metas(None),
        data: dvpn::instruction::UpdatePlan {
            price,
            duration_seconds,
            data_cap_gb,
            max_connections,
        }
        .data(),
    }
}

#[allow(clippy::too_many_arguments)]
pub fn update_plan_ix_as_grantee(
    grantee: &Pubkey,
    grant: &Pubkey,
    provider: Pubkey,
    plan_id: [u8; 32],
    price: Option<u64>,
    duration_seconds: Option<u32>,
    data_cap_gb: Option<u32>,
    max_connections: Option<u8>,
) -> Instruction {
    let plan = find_plan(&ID, &provider, &plan_id).0;
    update_plan_ix_by(
        grantee,
        Some(*grant),
        provider,
        plan,
        price,
        duration_seconds,
        data_cap_gb,
        max_connections,
    )
}

pub fn close_plan_ix(owner: &Pubkey, provider: Pubkey, plan_id: [u8; 32]) -> Instruction {
    let plan = find_plan(&ID, &provider, &plan_id).0;
    close_plan_ix_by(owner, None, provider, plan, *owner)
}

pub fn close_plan_ix_by(
    signer: &Pubkey,
    grant: Option<Pubkey>,
    provider: Pubkey,
    plan: Pubkey,
    rent_recipient: Pubkey,
) -> Instruction {
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::ClosePlan {
            signer: *signer,
            provider,
            grant,
            plan,
            rent_recipient,
        }
        .to_account_metas(None),
        data: dvpn::instruction::ClosePlan {}.data(),
    }
}

pub fn close_plan_ix_as_grantee(
    grantee: &Pubkey,
    grant: &Pubkey,
    provider: Pubkey,
    plan_id: [u8; 32],
    rent_recipient: Pubkey,
) -> Instruction {
    let plan = find_plan(&ID, &provider, &plan_id).0;
    close_plan_ix_by(grantee, Some(*grant), provider, plan, rent_recipient)
}
