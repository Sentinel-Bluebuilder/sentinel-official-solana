use anchor_lang::{InstructionData, ToAccountMetas, system_program};
use distributor_core::pda::{find_claim_bitmap, find_config, find_roots, find_vault_authority};
use distributor_mint::create::{TokenMetadataParams, create_mint_ixs};
use distributor_mint::supply::{
    create_token_account_ixs, initialize_metadata_ixs, mint_to_ix, seal_mint_ix,
};
use solana_instruction::{AccountMeta, Instruction};
use solana_pubkey::Pubkey;

pub struct CeremonyInputs {
    pub program_id: Pubkey,
    pub token_metadata: TokenMetadataParams,
    pub roots: Vec<[u8; 32]>,
    pub shard_leaf_counts: Vec<u32>,
    pub vault_amount: u64,
    pub treasury_amount: u64,
    pub expected_roots_commitment: [u8; 32],
}
pub use distributor_core::ceremony::CeremonyAddresses;

pub struct RentTable {
    pub mint_at_create_lamports: u64,
    pub mint_with_metadata_lamports: u64,
    pub token_account_lamports: u64,
}

#[derive(Debug, Clone)]
pub enum ResumeHint {
    RootsChunk {
        start_index: usize,
        chunk: Vec<[u8; 32]>,
    },
}

pub struct Step {
    pub name: String,
    pub ixs: Vec<Instruction>,
    pub extra_signers: Vec<Pubkey>,
    pub resume: Option<ResumeHint>,
}

fn instruction(program_id: Pubkey, accounts: Vec<AccountMeta>, data: Vec<u8>) -> Instruction {
    Instruction {
        program_id,
        accounts,
        data,
    }
}

fn program_data_of(program_id: &Pubkey) -> Pubkey {
    Pubkey::find_program_address(
        &[program_id.as_ref()],
        &anchor_lang::solana_program::bpf_loader_upgradeable::id(),
    )
    .0
}

#[allow(clippy::vec_init_then_push, clippy::expect_used)]
#[must_use]
pub fn build_ceremony_steps(
    inputs: &CeremonyInputs,
    keys: &CeremonyAddresses,
    rent: &RentTable,
    roots_per_chunk: usize,
) -> Vec<Step> {
    let program_id = inputs.program_id;
    let upgrade_authority = keys.upgrade_authority;
    let payer = keys.payer;
    let mint = keys.mint;
    let (config, _) = find_config(&program_id);
    let (roots_pda, _) = find_roots(&program_id);
    let (vault_authority, _) = find_vault_authority(&program_id);
    let program_data = program_data_of(&program_id);

    let mut steps = Vec::new();
    steps.push(Step {
        name: "distributor_create_mint".into(),
        ixs: create_mint_ixs(
            &payer,
            &mint,
            &upgrade_authority,
            &upgrade_authority,
            rent.mint_at_create_lamports,
        )
        .expect("ceremony's mint authority is never the zero address"),
        extra_signers: vec![mint],
        resume: None,
    });
    steps.push(Step {
        name: "distributor_initialize_metadata".into(),
        ixs: initialize_metadata_ixs(
            &payer,
            &mint,
            &upgrade_authority,
            &upgrade_authority,
            rent.mint_at_create_lamports,
            rent.mint_with_metadata_lamports,
            &inputs.token_metadata,
        ),
        extra_signers: vec![],
        resume: None,
    });
    steps.push(Step {
        name: "distributor_create_treasury".into(),
        ixs: create_token_account_ixs(
            &payer,
            &keys.treasury,
            &upgrade_authority,
            &mint,
            rent.token_account_lamports,
        )
        .expect("mint crate always passes the fixed token-2022 program id"),
        extra_signers: vec![keys.treasury],
        resume: None,
    });
    steps.push(Step {
        name: "distributor_create_vault".into(),
        ixs: create_token_account_ixs(
            &payer,
            &keys.vault,
            &vault_authority,
            &mint,
            rent.token_account_lamports,
        )
        .expect("mint crate always passes the fixed token-2022 program id"),
        extra_signers: vec![keys.vault],
        resume: None,
    });
    steps.push(Step {
        name: "distributor_mint_supply".into(),
        ixs: vec![
            mint_to_ix(
                &mint,
                &keys.treasury,
                &upgrade_authority,
                inputs.treasury_amount,
            )
            .expect("mint crate always passes the fixed token-2022 program id"),
            mint_to_ix(&mint, &keys.vault, &upgrade_authority, inputs.vault_amount)
                .expect("mint crate always passes the fixed token-2022 program id"),
            seal_mint_ix(&mint, &upgrade_authority)
                .expect("mint crate always passes the fixed token-2022 program id"),
        ],
        extra_signers: vec![],
        resume: None,
    });
    steps.push(Step {
        name: "distributor_initialize_config".into(),
        ixs: vec![instruction(
            program_id,
            distributor::accounts::InitializeConfig {
                upgrade_authority,
                program: program_id,
                program_data,
                config,
                mint,
                token_program: spl_token_2022_interface::id(),
                vault: keys.vault,
                treasury: keys.treasury,
                vault_authority,
                system_program: system_program::ID,
            }
            .to_account_metas(None),
            distributor::instruction::InitializeConfig {
                total_claimable: inputs.vault_amount,
            }
            .data(),
        )],
        extra_signers: vec![],
        resume: None,
    });
    steps.push(Step {
        name: "distributor_initialize_roots".into(),
        ixs: vec![instruction(
            program_id,
            distributor::accounts::InitializeRoots {
                upgrade_authority,
                program: program_id,
                program_data,
                config,
                roots: roots_pda,
                system_program: system_program::ID,
            }
            .to_account_metas(None),
            distributor::instruction::InitializeRoots {}.data(),
        )],
        extra_signers: vec![],
        resume: None,
    });

    for (index, chunk) in inputs.roots.chunks(roots_per_chunk).enumerate() {
        let start_index = index.saturating_mul(roots_per_chunk);

        #[allow(clippy::cast_possible_truncation)]
        let start_index_u16 = start_index as u16;
        steps.push(Step {
            name: format!("distributor_write_roots_chunk_{index}"),
            ixs: vec![instruction(
                program_id,
                distributor::accounts::WriteRootsChunk {
                    upgrade_authority,
                    program: program_id,
                    program_data,
                    config,
                    roots: roots_pda,
                }
                .to_account_metas(None),
                distributor::instruction::WriteRootsChunk {
                    start_index: start_index_u16,
                    chunk: chunk.to_vec(),
                }
                .data(),
            )],
            extra_signers: vec![],
            resume: Some(ResumeHint::RootsChunk {
                start_index,
                chunk: chunk.to_vec(),
            }),
        });
    }

    for (shard_id, &leaf_count) in inputs.shard_leaf_counts.iter().enumerate() {
        #[allow(clippy::cast_possible_truncation)]
        let shard_id = shard_id as u16;
        let (bitmap, _) = find_claim_bitmap(&program_id, shard_id);
        steps.push(Step {
            name: format!("distributor_initialize_bitmap_{shard_id}"),
            ixs: vec![instruction(
                program_id,
                distributor::accounts::InitializeBitmap {
                    upgrade_authority,
                    program: program_id,
                    program_data,
                    config,
                    bitmap,
                    system_program: system_program::ID,
                }
                .to_account_metas(None),
                distributor::instruction::InitializeBitmap {
                    shard_id,
                    leaf_count,
                }
                .data(),
            )],
            extra_signers: vec![],
            resume: None,
        });
    }
    steps
}

#[must_use]
pub fn build_finalize_genesis_step(inputs: &CeremonyInputs, keys: &CeremonyAddresses) -> Step {
    let program_id = inputs.program_id;
    let (config, _) = find_config(&program_id);
    let (roots_pda, _) = find_roots(&program_id);
    Step {
        name: "distributor_finalize_genesis".into(),
        ixs: vec![instruction(
            program_id,
            distributor::accounts::FinalizeGenesis {
                upgrade_authority: keys.upgrade_authority,
                program: program_id,
                program_data: program_data_of(&program_id),
                config,
                roots: roots_pda,
                mint: keys.mint,
                vault: keys.vault,
                treasury: keys.treasury,
            }
            .to_account_metas(None),
            distributor::instruction::FinalizeGenesis {
                expected_roots_commitment: inputs.expected_roots_commitment,
            }
            .data(),
        )],
        extra_signers: vec![],
        resume: None,
    }
}
