use crate::error::DvpnError;
use crate::events::{GrantClosed, GrantOpened, GrantUpdated};
use crate::state::Grant;
use anchor_lang::prelude::*;
use dvpn_core::permissions::is_valid_permissions;
use dvpn_core::seeds::GRANT_SEED;

#[derive(Accounts)]
#[instruction(grantee: Pubkey, target: Pubkey)]
pub struct OpenGrant<'info> {
    pub granter: Signer<'info>,
    #[account(mut)]
    pub rent_payer: Signer<'info>,
    #[account(
        init,
        payer = rent_payer,
        space = 8 + Grant::INIT_SPACE,
        seeds = [GRANT_SEED, granter.key().as_ref(), grantee.as_ref(), target.as_ref()],
        bump
    )]
    pub grant: Account<'info, Grant>,
    pub system_program: Program<'info, System>,
}

pub fn open_grant(
    ctx: Context<OpenGrant>,
    grantee: Pubkey,
    target: Pubkey,
    permissions: u64,
    expires_at: i64,
    allowance: u64,
    grant_rent_recipient: Pubkey,
) -> Result<()> {
    crate::rent::require_rent_recipient(
        &grant_rent_recipient,
        &ctx.accounts.granter.key(),
        &ctx.accounts.rent_payer.key(),
    )?;
    require!(grantee != Pubkey::default(), DvpnError::ParamOutOfRange);
    require_keys_neq!(
        grantee,
        ctx.accounts.granter.key(),
        DvpnError::ParamOutOfRange
    );
    require!(
        is_valid_permissions(permissions),
        DvpnError::ParamOutOfRange
    );
    let now = Clock::get()?.unix_timestamp;
    require!(
        expires_at == 0 || expires_at > now,
        DvpnError::ParamOutOfRange
    );
    let grant = &mut ctx.accounts.grant;
    grant.granter = ctx.accounts.granter.key();
    grant.grantee = grantee;
    grant.target = target;
    grant.permissions = permissions;
    grant.expires_at = expires_at;
    grant.allowance = allowance;
    grant.rent_recipient = grant_rent_recipient;
    grant.created_at = now;
    grant.bump = ctx.bumps.grant;
    grant.reserved = [0u8; 32];
    emit!(GrantOpened {
        grant: grant.key(),
        granter: grant.granter,
        grantee,
        target,
        permissions,
        expires_at,
        allowance,
        grant_rent_recipient: grant.rent_recipient,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct UpdateGrant<'info> {
    pub signer: Signer<'info>,
    #[account(mut, constraint = grant.granter == signer.key() @ DvpnError::Unauthorized)]
    pub grant: Account<'info, Grant>,
}

pub fn update_grant(
    ctx: Context<UpdateGrant>,
    permissions: Option<u64>,
    expires_at: Option<i64>,
    allowance: Option<u64>,
) -> Result<()> {
    let now = Clock::get()?.unix_timestamp;
    let grant = &mut ctx.accounts.grant;
    if let Some(permissions) = permissions {
        require!(
            is_valid_permissions(permissions),
            DvpnError::ParamOutOfRange
        );
        grant.permissions = permissions;
    }
    if let Some(expires_at) = expires_at {
        require!(
            expires_at == 0 || expires_at > now,
            DvpnError::ParamOutOfRange
        );
        grant.expires_at = expires_at;
    }
    if let Some(allowance) = allowance {
        grant.allowance = allowance;
    }
    emit!(GrantUpdated {
        grant: grant.key(),
        granter: grant.granter,
        grantee: grant.grantee,
        target: grant.target,
        permissions: grant.permissions,
        expires_at: grant.expires_at,
        allowance: grant.allowance,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct CloseGrant<'info> {
    pub signer: Signer<'info>,
    #[account(mut, close = rent_recipient)]
    pub grant: Account<'info, Grant>,
    #[doc = "CHECK: pinned to the stored rent recipient."]
    #[account(mut, address = grant.rent_recipient)]
    pub rent_recipient: UncheckedAccount<'info>,
}

pub fn close_grant(ctx: Context<CloseGrant>) -> Result<()> {
    let grant = &ctx.accounts.grant;
    let signer = ctx.accounts.signer.key();
    let now = Clock::get()?.unix_timestamp;
    require!(
        signer == grant.granter || signer == grant.grantee || grant.is_expired(now),
        DvpnError::Unauthorized
    );
    emit!(GrantClosed {
        grant: grant.key(),
        granter: grant.granter,
        grantee: grant.grantee,
        target: grant.target,
        closer: signer,
    });
    Ok(())
}
