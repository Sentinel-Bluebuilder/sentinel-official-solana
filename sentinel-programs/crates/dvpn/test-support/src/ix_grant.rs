use anchor_lang::{InstructionData, ToAccountMetas};
use dvpn::ID;
use dvpn_core::seeds::find_grant;
use solana_sdk::{instruction::Instruction, pubkey::Pubkey};

#[allow(clippy::too_many_arguments)]
pub fn open_grant_ix(
    granter: &Pubkey,
    rent_payer: &Pubkey,
    grantee: Pubkey,
    target: Pubkey,
    permissions: u64,
    expires_at: i64,
    allowance: u64,
    grant_rent_recipient: Pubkey,
) -> Instruction {
    let grant = find_grant(&ID, granter, &grantee, &target).0;
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::OpenGrant {
            granter: *granter,
            rent_payer: *rent_payer,
            grant,
            system_program: anchor_lang::solana_program::system_program::ID,
        }
        .to_account_metas(None),
        data: dvpn::instruction::OpenGrant {
            grantee,
            target,
            permissions,
            expires_at,
            allowance,
            grant_rent_recipient,
        }
        .data(),
    }
}

pub fn update_grant_ix(
    granter: &Pubkey,
    grant: Pubkey,
    permissions: Option<u64>,
    expires_at: Option<i64>,
    allowance: Option<u64>,
) -> Instruction {
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::UpdateGrant {
            signer: *granter,
            grant,
        }
        .to_account_metas(None),
        data: dvpn::instruction::UpdateGrant {
            permissions,
            expires_at,
            allowance,
        }
        .data(),
    }
}

pub fn close_grant_ix(signer: &Pubkey, grant: Pubkey, rent_recipient: Pubkey) -> Instruction {
    Instruction {
        program_id: ID,
        accounts: dvpn::accounts::CloseGrant {
            signer: *signer,
            grant,
            rent_recipient,
        }
        .to_account_metas(None),
        data: dvpn::instruction::CloseGrant {}.data(),
    }
}
