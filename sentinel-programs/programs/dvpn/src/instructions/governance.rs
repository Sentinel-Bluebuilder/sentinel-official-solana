use crate::error::DvpnError;
use crate::events::{
    AuthorityAccepted, AuthorityProposed, ConfigFeesSwept, ConfigInitialized, ParamsSet,
    PausedMaskSet, TreasurySet,
};
use crate::mint::require_allowed_mint;
use crate::program;
use crate::state::Config;
use anchor_lang::prelude::*;
use anchor_spl::token_2022;
use anchor_spl::token_interface::{Mint, TokenAccount, TokenInterface};
use dvpn_core::args::{ConfigParamsUpdate, InitialConfigParams};
use dvpn_core::constants::{
    MAX_FEE_BPS, MAX_GRACE_SECONDS, MAX_NODE_LISTING_TTL_SECONDS, MAX_UNBONDING_SECONDS,
    MIN_GRACE_SECONDS, MIN_NODE_LISTING_TTL_SECONDS, MIN_UNBONDING_SECONDS, PAUSE_MASK,
};
use dvpn_core::seeds::{CONFIG_SEED, vault_ata_address};

#[derive(Accounts)]
pub struct InitializeConfig<'info> {
    pub authority: Signer<'info>,
    #[account(constraint = program.programdata_address()? == Some(program_data.key()) @ DvpnError::Unauthorized)]
    pub program: Program<'info, program::Dvpn>,
    #[account(constraint = program_data.upgrade_authority_address == Some(authority.key()) @ DvpnError::Unauthorized)]
    pub program_data: Account<'info, ProgramData>,
    #[account(init, payer = rent_payer, space = Config::LEN, seeds = [CONFIG_SEED], bump)]
    pub config: Account<'info, Config>,
    #[account(constraint = *mint.to_account_info().owner == token_2022::ID @ DvpnError::InvalidMint)]
    pub mint: InterfaceAccount<'info, Mint>,
    #[account(
        constraint = vault.mint == mint.key() @ DvpnError::VaultNotAssociated,
        constraint = vault.owner == config.key() @ DvpnError::VaultNotAssociated,
    )]
    #[cfg_attr(
        feature = "idl-build",
        account(
            associated_token::mint = mint,
            associated_token::authority = config,
            associated_token::token_program = token_program
        )
    )]
    pub vault: InterfaceAccount<'info, TokenAccount>,
    #[account(
        constraint = treasury.mint == mint.key() @ DvpnError::InvalidTreasury,
        constraint = treasury.key() != vault.key() @ DvpnError::InvalidTreasury,
        constraint = treasury.owner != config.key() @ DvpnError::InvalidTreasury,
    )]
    pub treasury: InterfaceAccount<'info, TokenAccount>,
    #[account(address = token_2022::ID)]
    pub token_program: Interface<'info, TokenInterface>,
    #[account(mut)]
    pub rent_payer: Signer<'info>,
    pub system_program: Program<'info, System>,
}

pub fn initialize_config(
    ctx: Context<InitializeConfig>,
    params: InitialConfigParams,
) -> Result<()> {
    let expected_vault = vault_ata_address(&ctx.accounts.config.key(), &ctx.accounts.mint.key());
    require_keys_eq!(
        ctx.accounts.vault.key(),
        expected_vault,
        DvpnError::VaultNotAssociated
    );
    require!(
        ctx.accounts.vault_has_immutable_owner()?,
        DvpnError::VaultNotImmutable
    );
    require!(!ctx.accounts.vault.is_frozen(), DvpnError::VaultFrozen);

    let decimals = require_allowed_mint(&ctx.accounts.mint.to_account_info().try_borrow_data()?)?;

    require!(params.fee_bps <= MAX_FEE_BPS, DvpnError::FeeBpsOutOfRange);
    require!(
        (MIN_UNBONDING_SECONDS..=MAX_UNBONDING_SECONDS).contains(&params.unbonding_seconds),
        DvpnError::ParamOutOfRange
    );
    require!(
        (MIN_GRACE_SECONDS..=MAX_GRACE_SECONDS).contains(&params.channel_close_grace_seconds),
        DvpnError::ParamOutOfRange
    );
    require!(
        (MIN_GRACE_SECONDS..=MAX_GRACE_SECONDS).contains(&params.lease_close_grace_seconds),
        DvpnError::ParamOutOfRange
    );
    require!(
        (MIN_NODE_LISTING_TTL_SECONDS..=MAX_NODE_LISTING_TTL_SECONDS)
            .contains(&params.node_listing_ttl_seconds),
        DvpnError::ParamOutOfRange
    );
    require!(params.paused_mask <= PAUSE_MASK, DvpnError::ParamOutOfRange);

    let config = &mut ctx.accounts.config;
    config.authority = ctx.accounts.authority.key();
    config.pending_authority = Pubkey::default();
    config.mint = ctx.accounts.mint.key();
    config.decimals = decimals;
    config.vault = ctx.accounts.vault.key();
    config.treasury = ctx.accounts.treasury.key();
    config.fee_bps = params.fee_bps;
    config.min_node_bond = params.min_node_bond;
    config.min_provider_bond = params.min_provider_bond;
    config.unbonding_seconds = params.unbonding_seconds;
    config.channel_close_grace_seconds = params.channel_close_grace_seconds;
    config.lease_close_grace_seconds = params.lease_close_grace_seconds;
    config.node_listing_ttl_seconds = params.node_listing_ttl_seconds;
    config.paused_mask = params.paused_mask;
    config.bump = ctx.bumps.config;
    config.fees_accrued = 0;
    config.reserved = [0u8; 64];

    emit!(ConfigInitialized {
        authority: config.authority,
        mint: config.mint,
        vault: config.vault,
        treasury: config.treasury,
        fee_bps: config.fee_bps,
        min_node_bond: config.min_node_bond,
        min_provider_bond: config.min_provider_bond,
        unbonding_seconds: config.unbonding_seconds,
        channel_close_grace_seconds: config.channel_close_grace_seconds,
        lease_close_grace_seconds: config.lease_close_grace_seconds,
        node_listing_ttl_seconds: config.node_listing_ttl_seconds,
        paused_mask: config.paused_mask,
    });
    Ok(())
}

impl<'info> InitializeConfig<'info> {
    fn vault_has_immutable_owner(&self) -> Result<bool> {
        use spl_token_2022_interface::extension::{
            BaseStateWithExtensions, ExtensionType, StateWithExtensions,
        };
        use spl_token_2022_interface::state::Account as SplAccount;
        let info = self.vault.to_account_info();
        let data = info.try_borrow_data()?;
        let state = StateWithExtensions::<SplAccount>::unpack(&data)
            .map_err(|_| error!(DvpnError::VaultNotImmutable))?;
        Ok(state
            .get_extension_types()
            .map_err(|_| error!(DvpnError::VaultNotImmutable))?
            .contains(&ExtensionType::ImmutableOwner))
    }
}

#[derive(Accounts)]
pub struct SetParams<'info> {
    pub authority: Signer<'info>,
    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
}

pub fn set_params(ctx: Context<SetParams>, params: ConfigParamsUpdate) -> Result<()> {
    let config = &mut ctx.accounts.config;
    require_keys_eq!(
        ctx.accounts.authority.key(),
        config.authority,
        DvpnError::Unauthorized
    );
    if let Some(fee_bps) = params.fee_bps {
        require!(fee_bps <= MAX_FEE_BPS, DvpnError::FeeBpsOutOfRange);
        config.fee_bps = fee_bps;
    }
    if let Some(min_node_bond) = params.min_node_bond {
        config.min_node_bond = min_node_bond;
    }
    if let Some(min_provider_bond) = params.min_provider_bond {
        config.min_provider_bond = min_provider_bond;
    }
    if let Some(unbonding_seconds) = params.unbonding_seconds {
        require!(
            (MIN_UNBONDING_SECONDS..=MAX_UNBONDING_SECONDS).contains(&unbonding_seconds),
            DvpnError::ParamOutOfRange
        );
        config.unbonding_seconds = unbonding_seconds;
    }
    if let Some(grace_seconds) = params.channel_close_grace_seconds {
        require!(
            (MIN_GRACE_SECONDS..=MAX_GRACE_SECONDS).contains(&grace_seconds),
            DvpnError::ParamOutOfRange
        );
        config.channel_close_grace_seconds = grace_seconds;
    }
    if let Some(grace_seconds) = params.lease_close_grace_seconds {
        require!(
            (MIN_GRACE_SECONDS..=MAX_GRACE_SECONDS).contains(&grace_seconds),
            DvpnError::ParamOutOfRange
        );
        config.lease_close_grace_seconds = grace_seconds;
    }
    if let Some(ttl_seconds) = params.node_listing_ttl_seconds {
        require!(
            (MIN_NODE_LISTING_TTL_SECONDS..=MAX_NODE_LISTING_TTL_SECONDS).contains(&ttl_seconds),
            DvpnError::ParamOutOfRange
        );
        config.node_listing_ttl_seconds = ttl_seconds;
    }
    emit!(ParamsSet {
        fee_bps: config.fee_bps,
        min_node_bond: config.min_node_bond,
        min_provider_bond: config.min_provider_bond,
        unbonding_seconds: config.unbonding_seconds,
        channel_close_grace_seconds: config.channel_close_grace_seconds,
        lease_close_grace_seconds: config.lease_close_grace_seconds,
        node_listing_ttl_seconds: config.node_listing_ttl_seconds,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct SetTreasury<'info> {
    pub authority: Signer<'info>,
    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(
        constraint = treasury.mint == config.mint @ DvpnError::InvalidTreasury,
        constraint = treasury.key() != config.vault @ DvpnError::InvalidTreasury,
        constraint = treasury.owner != config.key() @ DvpnError::InvalidTreasury,
    )]
    pub treasury: InterfaceAccount<'info, TokenAccount>,
}

pub fn set_treasury(ctx: Context<SetTreasury>) -> Result<()> {
    require_keys_eq!(
        ctx.accounts.authority.key(),
        ctx.accounts.config.authority,
        DvpnError::Unauthorized
    );
    ctx.accounts.config.treasury = ctx.accounts.treasury.key();
    emit!(TreasurySet {
        treasury: ctx.accounts.config.treasury
    });
    Ok(())
}

#[derive(Accounts)]
pub struct SetPausedMask<'info> {
    pub authority: Signer<'info>,
    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
}

pub fn set_paused_mask(ctx: Context<SetPausedMask>, paused_mask: u8) -> Result<()> {
    require_keys_eq!(
        ctx.accounts.authority.key(),
        ctx.accounts.config.authority,
        DvpnError::Unauthorized
    );
    require!(paused_mask <= PAUSE_MASK, DvpnError::ParamOutOfRange);
    ctx.accounts.config.paused_mask = paused_mask;
    emit!(PausedMaskSet { paused_mask });
    Ok(())
}

#[derive(Accounts)]
pub struct ProposeAuthority<'info> {
    pub authority: Signer<'info>,
    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
}

pub fn propose_authority(ctx: Context<ProposeAuthority>, new_authority: Pubkey) -> Result<()> {
    require_keys_eq!(
        ctx.accounts.authority.key(),
        ctx.accounts.config.authority,
        DvpnError::Unauthorized
    );
    ctx.accounts.config.pending_authority = new_authority;
    emit!(AuthorityProposed {
        pending_authority: new_authority
    });
    Ok(())
}

#[derive(Accounts)]
pub struct AcceptAuthority<'info> {
    pub authority: Signer<'info>,
    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
}

pub fn accept_authority(ctx: Context<AcceptAuthority>) -> Result<()> {
    let config = &mut ctx.accounts.config;
    require!(
        config.pending_authority != Pubkey::default(),
        DvpnError::MissingPendingAuthority
    );
    require_keys_eq!(
        ctx.accounts.authority.key(),
        config.pending_authority,
        DvpnError::Unauthorized
    );
    config.authority = config.pending_authority;
    config.pending_authority = Pubkey::default();
    emit!(AuthorityAccepted {
        authority: config.authority
    });
    Ok(())
}

#[derive(Accounts)]
pub struct SweepConfigFees<'info> {
    pub signer: Signer<'info>,
    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(mut, address = config.vault)]
    pub vault: InterfaceAccount<'info, TokenAccount>,
    #[account(mut, address = config.treasury)]
    pub treasury: InterfaceAccount<'info, TokenAccount>,
    #[account(address = config.mint)]
    pub mint: InterfaceAccount<'info, Mint>,
    #[account(address = token_2022::ID)]
    pub token_program: Interface<'info, TokenInterface>,
}

pub fn sweep_config_fees(ctx: Context<SweepConfigFees>) -> Result<()> {
    let amount = ctx.accounts.config.fees_accrued;
    require!(amount > 0, DvpnError::FeesZero);
    crate::transfer::transfer_from_vault(
        &ctx.accounts.token_program,
        &ctx.accounts.vault,
        &ctx.accounts.mint,
        &ctx.accounts.treasury,
        &ctx.accounts.config.to_account_info(),
        ctx.accounts.config.bump,
        amount,
    )?;
    ctx.accounts.config.fees_accrued = 0;
    emit!(ConfigFeesSwept {
        treasury: ctx.accounts.treasury.key(),
        amount,
    });
    Ok(())
}
