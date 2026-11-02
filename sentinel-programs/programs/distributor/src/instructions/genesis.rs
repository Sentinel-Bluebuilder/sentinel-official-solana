use anchor_lang::prelude::*;
use anchor_spl::token_interface::spl_token_2022::extension::{
    BaseStateWithExtensions, ExtensionType, StateWithExtensions,
};
use anchor_spl::token_interface::spl_token_2022::state::Mint as SplMint;
use anchor_spl::token_interface::{Mint, TokenAccount, TokenInterface};
use distributor_core::consts::SHARD_COUNT;
use distributor_core::pda::{CLAIM_BITMAP_SEED, CONFIG_SEED, ROOTS_SEED, VAULT_AUTHORITY_SEED};
use distributor_merkle::sharded::MAX_SHARD_LEAF_COUNT;

use crate::error::DistributorError;
use crate::program;
use crate::state::{ClaimBitmap, Config, Roots};

pub const ALLOWED_MINT_EXTENSIONS: [ExtensionType; 3] = [
    ExtensionType::ConfidentialTransferMint,
    ExtensionType::MetadataPointer,
    ExtensionType::TokenMetadata,
];

pub fn require_allowed_mint_extensions(mint_data: &[u8]) -> Result<()> {
    let state = StateWithExtensions::<SplMint>::unpack(mint_data)
        .map_err(|_| error!(DistributorError::InvalidMint))?;
    let present = state
        .get_extension_types()
        .map_err(|_| error!(DistributorError::InvalidMint))?;
    for extension in present {
        require!(
            ALLOWED_MINT_EXTENSIONS.contains(&extension),
            DistributorError::MintExtensionForbidden
        );
    }
    Ok(())
}

#[derive(Accounts)]
pub struct InitializeConfig<'info> {
    #[account(mut)]
    pub upgrade_authority: Signer<'info>,
    #[account(constraint = program.programdata_address()? == Some(program_data.key()) @ DistributorError::Unauthorized)]
    pub program: Program<'info, program::Distributor>,
    #[account(constraint = program_data.upgrade_authority_address == Some(upgrade_authority.key()) @ DistributorError::Unauthorized)]
    pub program_data: Account<'info, ProgramData>,
    #[account(init, payer = upgrade_authority, space = 8 + Config::INIT_SPACE, seeds = [CONFIG_SEED], bump)]
    pub config: Account<'info, Config>,
    pub mint: InterfaceAccount<'info, Mint>,
    #[account(
        address = anchor_spl::token_2022::ID @ DistributorError::TokenProgramMismatch,
        constraint = token_program.key() == *mint.to_account_info().owner @ DistributorError::TokenProgramMismatch
    )]
    pub token_program: Interface<'info, TokenInterface>,
    #[doc = "CHECK: vault-authority PDA; bump captured into config."]
    #[account(seeds = [VAULT_AUTHORITY_SEED], bump)]
    pub vault_authority: UncheckedAccount<'info>,
    #[account(token::mint = mint, token::authority = vault_authority)]
    pub vault: InterfaceAccount<'info, TokenAccount>,
    #[account(
        token::mint = mint,
        constraint = treasury.key() != vault.key() @ DistributorError::InvalidTreasury
    )]
    pub treasury: InterfaceAccount<'info, TokenAccount>,
    pub system_program: Program<'info, System>,
}

pub fn initialize_config(ctx: Context<InitializeConfig>, total_claimable: u64) -> Result<()> {
    let config = &mut ctx.accounts.config;
    config.upgrade_authority = ctx.accounts.upgrade_authority.key();
    config.mint = ctx.accounts.mint.key();
    config.treasury = ctx.accounts.treasury.key();
    config.vault = ctx.accounts.vault.key();
    config.vault_authority_bump = ctx.bumps.vault_authority;
    config.roots_commitment = [0u8; 32];
    config.total_claimable = total_claimable;
    config.bitmap_count = 0;
    config.finalized = false;
    config.leaf_counts = [0u32; SHARD_COUNT as usize];
    config.bump = ctx.bumps.config;
    config.token_program = ctx.accounts.token_program.key();
    Ok(())
}

#[derive(Accounts)]
pub struct InitializeRoots<'info> {
    #[account(mut)]
    pub upgrade_authority: Signer<'info>,
    #[account(constraint = program.programdata_address()? == Some(program_data.key()) @ DistributorError::Unauthorized)]
    pub program: Program<'info, program::Distributor>,
    #[account(constraint = program_data.upgrade_authority_address == Some(upgrade_authority.key()) @ DistributorError::Unauthorized)]
    pub program_data: Account<'info, ProgramData>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump, constraint = !config.finalized @ DistributorError::ConfigAlreadyFinalized)]
    pub config: Account<'info, Config>,
    #[account(init, payer = upgrade_authority, space = Roots::LEN, seeds = [ROOTS_SEED], bump)]
    pub roots: AccountLoader<'info, Roots>,
    pub system_program: Program<'info, System>,
}

pub fn initialize_roots(ctx: Context<InitializeRoots>) -> Result<()> {
    ctx.accounts.roots.load_init()?;
    Ok(())
}

#[derive(Accounts)]
pub struct WriteRootsChunk<'info> {
    pub upgrade_authority: Signer<'info>,
    #[account(constraint = program.programdata_address()? == Some(program_data.key()) @ DistributorError::Unauthorized)]
    pub program: Program<'info, program::Distributor>,
    #[account(constraint = program_data.upgrade_authority_address == Some(upgrade_authority.key()) @ DistributorError::Unauthorized)]
    pub program_data: Account<'info, ProgramData>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump, constraint = !config.finalized @ DistributorError::ConfigAlreadyFinalized)]
    pub config: Account<'info, Config>,
    #[account(mut, seeds = [ROOTS_SEED], bump)]
    pub roots: AccountLoader<'info, Roots>,
}

pub fn write_roots_chunk(
    ctx: Context<WriteRootsChunk>,
    start_index: u16,
    chunk: Vec<[u8; 32]>,
) -> Result<()> {
    let start = start_index as usize;
    let end = start
        .checked_add(chunk.len())
        .ok_or_else(|| error!(DistributorError::ChunkOutOfRange))?;
    require!(
        end <= SHARD_COUNT as usize,
        DistributorError::ChunkOutOfRange
    );
    let mut roots = ctx.accounts.roots.load_mut()?;
    #[allow(clippy::indexing_slicing)]
    roots.shard_roots[start..end].copy_from_slice(&chunk);
    Ok(())
}

#[derive(Accounts)]
#[instruction(shard_id: u16, leaf_count: u32)]
pub struct InitializeBitmap<'info> {
    #[account(mut)]
    pub upgrade_authority: Signer<'info>,
    #[account(constraint = program.programdata_address()? == Some(program_data.key()) @ DistributorError::Unauthorized)]
    pub program: Program<'info, program::Distributor>,
    #[account(constraint = program_data.upgrade_authority_address == Some(upgrade_authority.key()) @ DistributorError::Unauthorized)]
    pub program_data: Account<'info, ProgramData>,
    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump, constraint = !config.finalized @ DistributorError::ConfigAlreadyFinalized)]
    pub config: Account<'info, Config>,
    #[account(
        init,
        payer = upgrade_authority,
        space = ClaimBitmap::clamped_account_space(leaf_count),
        seeds = [CLAIM_BITMAP_SEED, &shard_id.to_le_bytes()],
        bump
    )]
    pub bitmap: Account<'info, ClaimBitmap>,
    pub system_program: Program<'info, System>,
}

pub fn initialize_bitmap(
    ctx: Context<InitializeBitmap>,
    shard_id: u16,
    leaf_count: u32,
) -> Result<()> {
    require!(shard_id < SHARD_COUNT, DistributorError::ShardIdOutOfRange);
    require!(
        leaf_count as usize <= MAX_SHARD_LEAF_COUNT,
        DistributorError::ShardLeafCountTooLarge
    );
    let bitmap = &mut ctx.accounts.bitmap;
    bitmap.shard_id = shard_id;
    bitmap.bits = vec![0u8; (leaf_count as usize).div_ceil(8)];
    bitmap.bump = ctx.bumps.bitmap;
    require_eq!(
        bitmap.to_account_info().data_len(),
        ClaimBitmap::account_space(leaf_count),
        DistributorError::BitmapSizeMismatch
    );
    let config = &mut ctx.accounts.config;
    config.bitmap_count = config
        .bitmap_count
        .checked_add(1)
        .ok_or_else(|| error!(DistributorError::BitmapCountMismatch))?;
    #[allow(clippy::indexing_slicing)]
    {
        config.leaf_counts[shard_id as usize] = leaf_count;
    }
    Ok(())
}

#[derive(Accounts)]
pub struct FinalizeGenesis<'info> {
    #[account(mut)]
    pub upgrade_authority: Signer<'info>,
    #[account(constraint = program.programdata_address()? == Some(program_data.key()) @ DistributorError::Unauthorized)]
    pub program: Program<'info, program::Distributor>,
    #[account(constraint = program_data.upgrade_authority_address == Some(upgrade_authority.key()) @ DistributorError::Unauthorized)]
    pub program_data: Account<'info, ProgramData>,
    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump, constraint = !config.finalized @ DistributorError::ConfigAlreadyFinalized)]
    pub config: Account<'info, Config>,
    #[account(seeds = [ROOTS_SEED], bump)]
    pub roots: AccountLoader<'info, Roots>,
    #[account(address = config.mint)]
    pub mint: InterfaceAccount<'info, Mint>,
    #[account(address = config.vault)]
    pub vault: InterfaceAccount<'info, TokenAccount>,
    #[account(address = config.treasury, token::mint = mint)]
    pub treasury: InterfaceAccount<'info, TokenAccount>,
}

pub fn finalize_genesis(
    ctx: Context<FinalizeGenesis>,
    expected_roots_commitment: [u8; 32],
) -> Result<()> {
    let roots_commitment = {
        let roots = ctx.accounts.roots.load()?;
        distributor_merkle::roots_commitment(
            &roots.shard_roots,
            &ctx.accounts.config.leaf_counts,
            ctx.accounts.config.total_claimable,
        )
    };
    require!(
        roots_commitment == expected_roots_commitment,
        DistributorError::RootsCommitmentMismatch
    );
    ctx.accounts.config.roots_commitment = roots_commitment;

    require_eq!(
        ctx.accounts.vault.amount,
        ctx.accounts.config.total_claimable,
        DistributorError::VaultBalanceMismatch
    );
    let sum = ctx
        .accounts
        .treasury
        .amount
        .checked_add(ctx.accounts.vault.amount)
        .ok_or_else(|| error!(DistributorError::SupplyMismatch))?;
    require_eq!(
        sum,
        ctx.accounts.mint.supply,
        DistributorError::SupplyMismatch
    );

    require!(
        ctx.accounts.mint.mint_authority.is_none(),
        DistributorError::MintNotSealed
    );
    require!(
        ctx.accounts.mint.freeze_authority.is_none(),
        DistributorError::MintNotSealed
    );
    require_eq!(
        ctx.accounts.mint.decimals,
        distributor_core::consts::DECIMALS,
        DistributorError::MintDecimalsMismatch
    );

    require!(
        !ctx.accounts.vault.is_frozen()
            && ctx.accounts.vault.delegate.is_none()
            && ctx.accounts.vault.close_authority.is_none(),
        DistributorError::VaultNotClean
    );

    {
        let mint_ai = ctx.accounts.mint.to_account_info();
        let mint_data = mint_ai.try_borrow_data()?;
        require_allowed_mint_extensions(&mint_data)?;
    }

    require_eq!(
        ctx.accounts.config.bitmap_count,
        SHARD_COUNT,
        DistributorError::BitmapCountMismatch
    );

    ctx.accounts.config.finalized = true;
    Ok(())
}
