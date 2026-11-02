use anchor_lang::prelude::*;
use anchor_lang::system_program::{
    Allocate, Assign, CreateAccount, Transfer, allocate, assign, create_account, transfer,
};
use anchor_spl::associated_token::AssociatedToken;
use anchor_spl::token_interface::{Mint, TokenAccount, TokenInterface};
use distributor_core::consts::SHARD_COUNT;
use distributor_core::pda::{
    CLAIM_BITMAP_SEED, CONFIG_SEED, ROOTS_SEED, VAULT_AUTHORITY_SEED, VESTING_STATE_SEED,
};

use crate::bitmap::{BitError, try_set};
use crate::error::DistributorError;
use crate::events::LeafClaimed;
use crate::state::{ClaimBitmap, Config, Roots, ScheduleBuffer};
use crate::transfer::vault_transfer;
use crate::verify::{VerifyInputs, recover_and_verify};

#[derive(AnchorSerialize, AnchorDeserialize, Clone)]
pub struct ClaimArgs {
    pub shard_id: u16,
    pub leaf_index: u32,
    pub bech32_address: String,
    pub liquid_amount: u64,
    pub locked_amount: u64,
    pub vesting_schedule_hash: [u8; 32],
    pub proof: Vec<[u8; 32]>,
    pub signature: [u8; 64],
    pub recovery_id: u8,
    pub schedule: Option<distributor_vesting::schedule::Schedule>,
}

#[derive(Accounts)]
#[instruction(args: ClaimArgs)]
pub struct Claim<'info> {
    #[account(mut)]
    pub rent_payer: Signer<'info>,
    #[doc = "CHECK: ATA authority; validated non-zero because the choice is permanent"]
    #[account(constraint = beneficiary.key() != Pubkey::default() @ DistributorError::InvalidBeneficiary)]
    pub beneficiary: UncheckedAccount<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump, constraint = config.finalized @ DistributorError::ConfigNotFinalized)]
    pub config: Box<Account<'info, Config>>,
    #[account(seeds = [ROOTS_SEED], bump)]
    pub roots: AccountLoader<'info, Roots>,
    #[account(
        mut,
        seeds = [CLAIM_BITMAP_SEED, &args.shard_id.to_le_bytes()],
        bump = bitmap.bump,
        constraint = bitmap.shard_id == args.shard_id @ DistributorError::ShardIdMismatch
    )]
    pub bitmap: Box<Account<'info, ClaimBitmap>>,
    #[account(mut, address = config.vault)]
    pub vault: Box<InterfaceAccount<'info, TokenAccount>>,
    #[doc = "CHECK: vault authority PDA; bump from config."]
    #[account(seeds = [VAULT_AUTHORITY_SEED], bump = config.vault_authority_bump)]
    pub vault_authority: UncheckedAccount<'info>,
    #[account(address = config.mint)]
    pub mint: Box<InterfaceAccount<'info, Mint>>,
    #[account(
        init_if_needed,
        payer = rent_payer,
        associated_token::mint = mint,
        associated_token::authority = beneficiary,
        associated_token::token_program = token_program
    )]
    pub beneficiary_token: Box<InterfaceAccount<'info, TokenAccount>>,
    #[account(address = config.token_program)]
    pub token_program: Interface<'info, TokenInterface>,
    pub associated_token_program: Program<'info, AssociatedToken>,
    pub system_program: Program<'info, System>,
    #[doc = "CHECK: VestingState PDA; created via a pre-fund-immune init for any locked"]
    #[account(
        mut,
        seeds = [VESTING_STATE_SEED, &args.shard_id.to_le_bytes(), &args.leaf_index.to_le_bytes()],
        bump
    )]
    pub vesting_state: UncheckedAccount<'info>,
    #[account(mut)]
    pub schedule_buffer: Option<Account<'info, ScheduleBuffer>>,
}

pub fn claim(ctx: Context<Claim>, mut args: ClaimArgs) -> Result<()> {
    require!(
        args.shard_id < SHARD_COUNT,
        DistributorError::ShardIdOutOfRange
    );

    let root = {
        let roots = ctx.accounts.roots.load()?;
        *roots
            .shard_roots
            .get(args.shard_id as usize)
            .ok_or_else(|| error!(DistributorError::ShardIdOutOfRange))?
    };

    let bech32_address = distributor_core::Bech32Address::parse(&args.bech32_address)
        .map_err(|_| error!(DistributorError::InvalidBech32Address))?;
    let beneficiary =
        distributor_core::Beneficiary::from_public_key(ctx.accounts.beneficiary.key());
    let roots_commitment =
        distributor_core::RootsCommitment::from_bytes(ctx.accounts.config.roots_commitment);
    let program_id = distributor_core::ProgramId::from_public_key(crate::ID);

    let verified = recover_and_verify(&VerifyInputs {
        beneficiary: &beneficiary,
        roots_commitment: &roots_commitment,
        program_id: &program_id,
        shard_id: args.shard_id,
        leaf_index: args.leaf_index,
        bech32_address: &bech32_address,
        liquid_amount: args.liquid_amount,
        locked_amount: args.locked_amount,
        vesting_schedule_hash: &args.vesting_schedule_hash,
        proof: &args.proof,
        root: &root,
        signature: &args.signature,
        recovery_id: args.recovery_id,
    })?;

    try_set(&mut ctx.accounts.bitmap.bits, args.leaf_index).map_err(|error| match error {
        BitError::AlreadySet => DistributorError::LeafAlreadyClaimed,
        BitError::OutOfRange => DistributorError::LeafIndexOutOfRange,
    })?;

    let mut liquid_to_send = args.liquid_amount;
    let mut folded = false;

    if args.locked_amount > 0 {
        let schedule: distributor_vesting::schedule::Schedule =
            match (args.schedule.take(), ctx.accounts.schedule_buffer.as_ref()) {
                (Some(_), Some(_)) => return err!(DistributorError::UnexpectedSchedule),
                (Some(schedule), None) => {
                    require!(
                        schedule.hash() == args.vesting_schedule_hash,
                        DistributorError::ScheduleHashMismatch
                    );
                    schedule
                }
                (None, Some(buf)) => {
                    let (tag, shard, index, rent_payer_bytes) =
                        distributor_core::pda::schedule_buffer_seeds(
                            args.shard_id,
                            args.leaf_index,
                            &buf.rent_payer,
                        );
                    let expected = Pubkey::create_program_address(
                        &[&tag, &shard, &index, &rent_payer_bytes, &[buf.bump]],
                        &crate::ID,
                    )
                    .map_err(|_| error!(DistributorError::BufferSeedsMismatch))?;
                    require_keys_eq!(buf.key(), expected, DistributorError::BufferSeedsMismatch);
                    require!(buf.finalized, DistributorError::BufferNotFinalized);

                    require!(
                        distributor_core::hash::keccak256(&[&buf.schedule_bytes])
                            == args.vesting_schedule_hash,
                        DistributorError::ScheduleHashMismatch
                    );
                    distributor_vesting::schedule::Schedule::decode_preimage(&buf.schedule_bytes)
                        .map_err(|_| error!(DistributorError::InvalidScheduleBytes))?
                }
                (None, None) => return err!(DistributorError::MissingSchedule),
            };

        let validated = distributor_vesting::unlock::check_schedule(&schedule, args.locked_amount)
            .map_err(|_| error!(DistributorError::InvalidSchedule))?;

        let now = Clock::get()?.unix_timestamp;
        if distributor_vesting::unlock::unlocked_amount(&validated, now) == args.locked_amount {
            liquid_to_send = liquid_to_send
                .checked_add(args.locked_amount)
                .ok_or_else(|| error!(DistributorError::ArithmeticOverflow))?;
            folded = true;
        } else {
            create_vesting_state(&ctx, &args, verified.leaf_public_key, schedule)?;
        }

        if let Some(buf) = ctx.accounts.schedule_buffer.as_ref()
            && buf.rent_payer == ctx.accounts.rent_payer.key()
        {
            buf.close(ctx.accounts.rent_payer.to_account_info())?;
        }
    } else {
        require!(
            args.schedule.is_none() && ctx.accounts.schedule_buffer.is_none(),
            DistributorError::UnexpectedSchedule
        );
    }

    if liquid_to_send > 0 {
        vault_transfer(
            &ctx.accounts.token_program,
            &ctx.accounts.vault,
            &ctx.accounts.mint,
            &ctx.accounts.beneficiary_token,
            &ctx.accounts.vault_authority.to_account_info(),
            ctx.accounts.config.vault_authority_bump,
            liquid_to_send,
        )?;
    }

    emit!(LeafClaimed {
        shard_id: args.shard_id,
        leaf_index: args.leaf_index,
        beneficiary: ctx.accounts.beneficiary.key(),
        liquid_amount: args.liquid_amount,
        locked_amount: args.locked_amount,
        folded,
    });
    Ok(())
}

fn create_vesting_state(
    ctx: &Context<Claim>,
    args: &ClaimArgs,
    leaf_public_key: [u8; 33],
    schedule: distributor_vesting::schedule::Schedule,
) -> Result<()> {
    #[allow(clippy::cast_possible_truncation)]
    let space = crate::state::VestingState::account_space(
        args.bech32_address.len() as u32,
        schedule.periods.len() as u32,
    );
    let rent = Rent::get()?.minimum_balance(space);
    let bump = ctx.bumps.vesting_state;
    let seeds: &[&[u8]] = &[
        VESTING_STATE_SEED,
        &args.shard_id.to_le_bytes(),
        &args.leaf_index.to_le_bytes(),
        &[bump],
    ];
    let vesting_state_info = ctx.accounts.vesting_state.to_account_info();
    let system_program_id = ctx.accounts.system_program.key();

    if vesting_state_info.lamports() == 0 {
        create_account(
            CpiContext::new_with_signer(
                system_program_id,
                CreateAccount {
                    from: ctx.accounts.rent_payer.to_account_info(),
                    to: vesting_state_info.clone(),
                },
                &[seeds],
            ),
            rent,
            space as u64,
            &crate::ID,
        )?;
    } else {
        require_keys_eq!(
            *vesting_state_info.owner,
            system_program_id,
            DistributorError::LeafAlreadyClaimed
        );
        require!(
            vesting_state_info.data_is_empty(),
            DistributorError::LeafAlreadyClaimed
        );
        let needed = rent.saturating_sub(vesting_state_info.lamports());
        if needed > 0 {
            transfer(
                CpiContext::new(
                    system_program_id,
                    Transfer {
                        from: ctx.accounts.rent_payer.to_account_info(),
                        to: vesting_state_info.clone(),
                    },
                ),
                needed,
            )?;
        }
        allocate(
            CpiContext::new_with_signer(
                system_program_id,
                Allocate {
                    account_to_allocate: vesting_state_info.clone(),
                },
                &[seeds],
            ),
            space as u64,
        )?;
        assign(
            CpiContext::new_with_signer(
                system_program_id,
                Assign {
                    account_to_assign: vesting_state_info.clone(),
                },
                &[seeds],
            ),
            &crate::ID,
        )?;
    }

    let vesting_state = crate::state::VestingState {
        beneficiary: ctx.accounts.beneficiary.key(),
        bech32_address: args.bech32_address.clone(),
        compressed_public_key: leaf_public_key,
        total: args.locked_amount,
        withdrawn: 0,
        schedule,
        rent_payer: ctx.accounts.rent_payer.key(),
        bump,
    };
    let mut data = vesting_state_info.try_borrow_mut_data()?;
    let mut cursor: &mut [u8] = &mut data;
    vesting_state.try_serialize(&mut cursor)?;
    Ok(())
}
