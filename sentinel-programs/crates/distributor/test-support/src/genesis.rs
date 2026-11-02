use distributor_core::ceremony::CeremonyAddresses;
use litesvm::LiteSVM;
use solana_program_pack::Pack;
use solana_sdk::{instruction::Instruction, pubkey::Pubkey, signature::Keypair, signer::Signer};
use spl_token_2022_interface::instruction::AuthorityType;
use spl_token_2022_interface::state::{Account as SplAccount, Mint as SplMint};

use crate::assert::assert_tx_within_limit;
use crate::cosmos::CosmosKey;
use crate::deploy::{PROGRAM_ID, deploy_upgradeable_distributor};
use crate::instructions::{
    SCHEDULE_CHUNK_BYTES, ata, finalize_genesis_ix, initialize_bitmap_ix, initialize_config_ix,
    initialize_roots_ix, stage_finalize_ix, stage_init_ix, stage_write_ix,
    withdraw_vested_ix_with_program_id, write_roots_chunk_ix,
};
use crate::send::send;
use test_support::read_token_amount;

pub struct MintAccounts {
    pub mint: Pubkey,
    pub treasury: Pubkey,
    pub vault: Pubkey,
    pub vault_authority: Pubkey,
}

struct MintSpec {
    treasury_amount: u64,
    vault_amount: u64,
    decimals: u8,
    seal: bool,
    extra_holder_amount: Option<u64>,
}

fn build_mint_from_spec(
    svm: &mut LiteSVM,
    upgrade_authority: &Keypair,
    program_id: &Pubkey,
    spec: &MintSpec,
) -> MintAccounts {
    let token_program = spl_token_2022_interface::id();
    let mint_keypair = Keypair::new();
    let treasury_keypair = Keypair::new();
    let vault_keypair = Keypair::new();
    let holder_keypair = Keypair::new();
    let (vault_authority, _) =
        Pubkey::find_program_address(&[distributor_core::pda::VAULT_AUTHORITY_SEED], program_id);

    let mint_rent = svm.minimum_balance_for_rent_exemption(SplMint::LEN);
    let account_rent = svm.minimum_balance_for_rent_exemption(SplAccount::LEN);

    let mut ixs = vec![
        solana_system_interface::instruction::create_account(
            &upgrade_authority.pubkey(),
            &mint_keypair.pubkey(),
            mint_rent,
            SplMint::LEN as u64,
            &token_program,
        ),
        spl_token_2022_interface::instruction::initialize_mint2(
            &token_program,
            &mint_keypair.pubkey(),
            &upgrade_authority.pubkey(),
            None,
            spec.decimals,
        )
        .expect("initialize_mint2 instruction should build"),
    ];
    let mut token_accounts = vec![
        (
            &treasury_keypair,
            upgrade_authority.pubkey(),
            spec.treasury_amount,
        ),
        (&vault_keypair, vault_authority, spec.vault_amount),
    ];
    if let Some(extra) = spec.extra_holder_amount {
        token_accounts.push((&holder_keypair, upgrade_authority.pubkey(), extra));
    }
    for (account, owner, _) in &token_accounts {
        ixs.push(solana_system_interface::instruction::create_account(
            &upgrade_authority.pubkey(),
            &account.pubkey(),
            account_rent,
            SplAccount::LEN as u64,
            &token_program,
        ));
        ixs.push(
            spl_token_2022_interface::instruction::initialize_account3(
                &token_program,
                &account.pubkey(),
                &mint_keypair.pubkey(),
                owner,
            )
            .expect("initialize_account3 instruction should build"),
        );
    }
    for (account, _, amount) in &token_accounts {
        ixs.push(
            spl_token_2022_interface::instruction::mint_to(
                &token_program,
                &mint_keypair.pubkey(),
                &account.pubkey(),
                &upgrade_authority.pubkey(),
                &[],
                *amount,
            )
            .expect("mint_to instruction should build"),
        );
    }
    if spec.seal {
        ixs.push(
            spl_token_2022_interface::instruction::set_authority(
                &token_program,
                &mint_keypair.pubkey(),
                None,
                AuthorityType::MintTokens,
                &upgrade_authority.pubkey(),
                &[],
            )
            .expect("set_authority instruction should build"),
        );
    }

    let mut signers: Vec<&Keypair> = vec![
        upgrade_authority,
        &mint_keypair,
        &treasury_keypair,
        &vault_keypair,
    ];
    if spec.extra_holder_amount.is_some() {
        signers.push(&holder_keypair);
    }
    let transaction = txv1::build_signed(
        &upgrade_authority.pubkey(),
        &ixs,
        &signers,
        svm.latest_blockhash(),
        &crate::send::HARNESS_TX_CONFIG,
    )
    .expect("mint setup transaction should compile and sign");
    svm.send_transaction(transaction)
        .expect("mint setup transaction should succeed");

    MintAccounts {
        mint: mint_keypair.pubkey(),
        treasury: treasury_keypair.pubkey(),
        vault: vault_keypair.pubkey(),
        vault_authority,
    }
}

fn build_mint_and_accounts(
    svm: &mut LiteSVM,
    upgrade_authority: &Keypair,
    treasury_amount: u64,
    vault_amount: u64,
    program_id: &Pubkey,
    seal: bool,
) -> MintAccounts {
    build_mint_from_spec(
        svm,
        upgrade_authority,
        program_id,
        &MintSpec {
            treasury_amount,
            vault_amount,
            decimals: distributor_core::consts::DECIMALS,
            seal,
            extra_holder_amount: None,
        },
    )
}

pub fn mint_and_accounts(
    svm: &mut LiteSVM,
    upgrade_authority: &Keypair,
    treasury_amount: u64,
    vault_amount: u64,
    program_id: &Pubkey,
) -> MintAccounts {
    build_mint_and_accounts(
        svm,
        upgrade_authority,
        treasury_amount,
        vault_amount,
        program_id,
        true,
    )
}

pub fn mint_and_accounts_unsealed(
    svm: &mut LiteSVM,
    upgrade_authority: &Keypair,
    treasury_amount: u64,
    vault_amount: u64,
    program_id: &Pubkey,
) -> MintAccounts {
    build_mint_and_accounts(
        svm,
        upgrade_authority,
        treasury_amount,
        vault_amount,
        program_id,
        false,
    )
}

pub fn mint_and_accounts_with_extensions(
    svm: &mut LiteSVM,
    upgrade_authority: &Keypair,
    treasury_amount: u64,
    vault_amount: u64,
    program_id: &Pubkey,
    extensions: &[spl_token_2022_interface::extension::ExtensionType],
    pre_init_ixs: impl FnOnce(&Pubkey, &Pubkey) -> Vec<Instruction>,
) -> MintAccounts {
    let token_program = spl_token_2022_interface::id();
    let mint_keypair = Keypair::new();
    let treasury_keypair = Keypair::new();
    let vault_keypair = Keypair::new();
    let (vault_authority, _) =
        Pubkey::find_program_address(&[distributor_core::pda::VAULT_AUTHORITY_SEED], program_id);

    let mint_space =
        spl_token_2022_interface::extension::ExtensionType::try_calculate_account_len::<SplMint>(
            extensions,
        )
        .expect("extension set should size");
    let mint_rent = svm.minimum_balance_for_rent_exemption(mint_space);
    #[allow(deprecated)]
    let account_space =
        spl_token_2022_interface::extension::ExtensionType::try_calculate_account_len::<SplAccount>(
            &spl_token_2022_interface::extension::ExtensionType::get_required_init_account_extensions(
                extensions,
            ),
        )
        .expect("required account extension set should size");
    let account_rent = svm.minimum_balance_for_rent_exemption(account_space);

    let mut ixs = vec![solana_system_interface::instruction::create_account(
        &upgrade_authority.pubkey(),
        &mint_keypair.pubkey(),
        mint_rent,
        mint_space as u64,
        &token_program,
    )];
    ixs.extend(pre_init_ixs(
        &mint_keypair.pubkey(),
        &upgrade_authority.pubkey(),
    ));
    ixs.push(
        spl_token_2022_interface::instruction::initialize_mint2(
            &token_program,
            &mint_keypair.pubkey(),
            &upgrade_authority.pubkey(),
            None,
            distributor_core::consts::DECIMALS,
        )
        .expect("initialize_mint2 instruction should build"),
    );
    for (account, owner) in [
        (&treasury_keypair, upgrade_authority.pubkey()),
        (&vault_keypair, vault_authority),
    ] {
        ixs.push(solana_system_interface::instruction::create_account(
            &upgrade_authority.pubkey(),
            &account.pubkey(),
            account_rent,
            account_space as u64,
            &token_program,
        ));
        ixs.push(
            spl_token_2022_interface::instruction::initialize_account3(
                &token_program,
                &account.pubkey(),
                &mint_keypair.pubkey(),
                &owner,
            )
            .expect("initialize_account3 instruction should build"),
        );
    }
    for (account, amount) in [
        (&treasury_keypair, treasury_amount),
        (&vault_keypair, vault_amount),
    ] {
        ixs.push(
            spl_token_2022_interface::instruction::mint_to(
                &token_program,
                &mint_keypair.pubkey(),
                &account.pubkey(),
                &upgrade_authority.pubkey(),
                &[],
                amount,
            )
            .expect("mint_to instruction should build"),
        );
    }
    ixs.push(
        spl_token_2022_interface::instruction::set_authority(
            &token_program,
            &mint_keypair.pubkey(),
            None,
            AuthorityType::MintTokens,
            &upgrade_authority.pubkey(),
            &[],
        )
        .expect("set_authority instruction should build"),
    );

    let transaction = txv1::build_signed(
        &upgrade_authority.pubkey(),
        &ixs,
        &[
            upgrade_authority,
            &mint_keypair,
            &treasury_keypair,
            &vault_keypair,
        ],
        svm.latest_blockhash(),
        &crate::send::HARNESS_TX_CONFIG,
    )
    .expect("extension mint setup transaction should compile and sign");
    svm.send_transaction(transaction)
        .expect("extension mint setup transaction should succeed");

    MintAccounts {
        mint: mint_keypair.pubkey(),
        treasury: treasury_keypair.pubkey(),
        vault: vault_keypair.pubkey(),
        vault_authority,
    }
}

pub fn mint_extension_types(
    svm: &LiteSVM,
    mint: &Pubkey,
) -> Vec<spl_token_2022_interface::extension::ExtensionType> {
    use spl_token_2022_interface::extension::{BaseStateWithExtensions, StateWithExtensions};
    let account = svm.get_account(mint).expect("mint account exists");
    StateWithExtensions::<SplMint>::unpack(&account.data)
        .expect("mint should unpack")
        .get_extension_types()
        .expect("mint TLV should parse")
}

fn mint_and_accounts_oversupplied(
    svm: &mut LiteSVM,
    upgrade_authority: &Keypair,
    treasury_amount: u64,
    vault_amount: u64,
    program_id: &Pubkey,
    extra: u64,
) -> MintAccounts {
    build_mint_from_spec(
        svm,
        upgrade_authority,
        program_id,
        &MintSpec {
            treasury_amount,
            vault_amount,
            decimals: distributor_core::consts::DECIMALS,
            seal: true,
            extra_holder_amount: Some(extra),
        },
    )
}

pub fn genesis_roots() -> [[u8; 32]; distributor_core::consts::SHARD_COUNT as usize] {
    let mut roots = [[0u8; 32]; distributor_core::consts::SHARD_COUNT as usize];
    for (index, root) in roots.iter_mut().enumerate() {
        root[0] = (index as u8).wrapping_add(1);
        root[1] = 0xa5;
    }
    roots
}

pub fn genesis_up_to_bitmaps_with(
    svm: &mut LiteSVM,
    treasury_amount: u64,
    vault_amount: u64,
    total_claimable: u64,
    build_mint: impl FnOnce(&mut LiteSVM, &Keypair, u64, u64, &Pubkey) -> MintAccounts,
) -> (
    Keypair,
    MintAccounts,
    [[u8; 32]; distributor_core::consts::SHARD_COUNT as usize],
) {
    let upgrade_authority = Keypair::new();
    svm.airdrop(&upgrade_authority.pubkey(), 100_000_000_000)
        .unwrap();
    deploy_upgradeable_distributor(svm, &upgrade_authority);
    let mint_accounts = build_mint(
        svm,
        &upgrade_authority,
        treasury_amount,
        vault_amount,
        &PROGRAM_ID,
    );
    send(
        svm,
        &upgrade_authority,
        initialize_config_ix(&upgrade_authority, &mint_accounts, total_claimable),
    );
    send(
        svm,
        &upgrade_authority,
        initialize_roots_ix(&upgrade_authority),
    );
    let roots = genesis_roots();
    for (index, chunk) in roots.chunks(26).enumerate() {
        send(
            svm,
            &upgrade_authority,
            write_roots_chunk_ix(&upgrade_authority, (index * 26) as u16, chunk),
        );
    }
    for shard in 0..distributor_core::consts::SHARD_COUNT {
        send(
            svm,
            &upgrade_authority,
            initialize_bitmap_ix(&upgrade_authority, shard, GENESIS_BITMAP_LEAF_COUNT),
        );
    }
    (upgrade_authority, mint_accounts, roots)
}

pub fn genesis_up_to_bitmaps(
    svm: &mut LiteSVM,
    treasury_amount: u64,
    vault_amount: u64,
    total_claimable: u64,
) -> (
    Keypair,
    MintAccounts,
    [[u8; 32]; distributor_core::consts::SHARD_COUNT as usize],
) {
    genesis_up_to_bitmaps_with(
        svm,
        treasury_amount,
        vault_amount,
        total_claimable,
        mint_and_accounts,
    )
}

pub fn genesis_up_to_bitmaps_unsealed(
    svm: &mut LiteSVM,
    treasury_amount: u64,
    vault_amount: u64,
    total_claimable: u64,
) -> (
    Keypair,
    MintAccounts,
    [[u8; 32]; distributor_core::consts::SHARD_COUNT as usize],
) {
    genesis_up_to_bitmaps_with(
        svm,
        treasury_amount,
        vault_amount,
        total_claimable,
        mint_and_accounts_unsealed,
    )
}

pub fn genesis_up_to_bitmaps_oversupplied(
    svm: &mut LiteSVM,
    treasury_amount: u64,
    vault_amount: u64,
    total_claimable: u64,
    extra: u64,
) -> (
    Keypair,
    MintAccounts,
    [[u8; 32]; distributor_core::consts::SHARD_COUNT as usize],
) {
    genesis_up_to_bitmaps_with(
        svm,
        treasury_amount,
        vault_amount,
        total_claimable,
        move |svm, upgrade_authority, treasury_amount, vault_amount, program_id| {
            mint_and_accounts_oversupplied(
                svm,
                upgrade_authority,
                treasury_amount,
                vault_amount,
                program_id,
                extra,
            )
        },
    )
}

pub const GENESIS_BITMAP_LEAF_COUNT: u32 = 8;

pub fn genesis_leaf_counts() -> [u32; distributor_core::consts::SHARD_COUNT as usize] {
    [GENESIS_BITMAP_LEAF_COUNT; distributor_core::consts::SHARD_COUNT as usize]
}

pub fn genesis_roots_commitment(roots: &[[u8; 32]], total_claimable: u64) -> [u8; 32] {
    distributor_merkle::roots_commitment(roots, &genesis_leaf_counts(), total_claimable)
}

pub fn continuous_schedule(
    total: u64,
    start_at: i64,
    end_at: i64,
) -> distributor_vesting::schedule::Schedule {
    distributor_vesting::schedule::Schedule {
        kind: distributor_vesting::schedule::ScheduleKind::Continuous,
        start_at,
        end_at,
        periods: vec![],
        total,
    }
}

pub struct SingleLeafSetup {
    pub shard_id: u16,
    pub leaf_index: u32,
    pub bech32_address: String,
    pub vesting_schedule_hash: [u8; 32],
    pub proof: Vec<[u8; 32]>,
    pub roots_commitment: [u8; 32],
    pub vault: Pubkey,
    pub mint: Pubkey,
}

impl SingleLeafSetup {
    pub fn beneficiary_token(&self, beneficiary: &Pubkey) -> Pubkey {
        spl_associated_token_account_interface::address::get_associated_token_address_with_program_id(
            beneficiary,
            &self.mint,
            &spl_token_2022_interface::id(),
        )
    }
}

enum LeafKind {
    Revealed,
    Unrevealed,
}

pub fn build_single_leaf_setup(
    svm: &mut LiteSVM,
    upgrade_authority: &Keypair,
    cosmos: &CosmosKey,
    liquid_amount: u64,
    locked_amount: u64,
) -> SingleLeafSetup {
    build_single_leaf_setup_of(
        svm,
        upgrade_authority,
        cosmos,
        liquid_amount,
        locked_amount,
        LeafKind::Revealed,
    )
}

pub fn build_unrevealed_leaf_setup(
    svm: &mut LiteSVM,
    upgrade_authority: &Keypair,
    cosmos: &CosmosKey,
    liquid_amount: u64,
    locked_amount: u64,
) -> SingleLeafSetup {
    build_single_leaf_setup_of(
        svm,
        upgrade_authority,
        cosmos,
        liquid_amount,
        locked_amount,
        LeafKind::Unrevealed,
    )
}

fn build_single_leaf_setup_of(
    svm: &mut LiteSVM,
    upgrade_authority: &Keypair,
    cosmos: &CosmosKey,
    liquid_amount: u64,
    locked_amount: u64,
    kind: LeafKind,
) -> SingleLeafSetup {
    let compressed_public_key = match kind {
        LeafKind::Revealed => Some(cosmos.compressed_public_key()),
        LeafKind::Unrevealed => None,
    };
    let record = distributor_merkle::LedgerRecord {
        compressed_public_key,
        bech32_address: cosmos.bech32_address(),
        liquid_amount,
        locked_amount,
        vesting_schedule_hash: distributor_vesting::schedule::ZERO_SCHEDULE_HASH,
    };
    let tree = distributor_merkle::build_sharded_tree(vec![record])
        .expect("single-leaf tree should build");
    let leaf = &tree.leaves[0];

    deploy_upgradeable_distributor(svm, upgrade_authority);
    let total_claimable = liquid_amount + locked_amount;
    let treasury_amount = 1_000_000u64;
    let mint_accounts = mint_and_accounts(
        svm,
        upgrade_authority,
        treasury_amount,
        total_claimable,
        &PROGRAM_ID,
    );

    send(
        svm,
        upgrade_authority,
        initialize_config_ix(upgrade_authority, &mint_accounts, total_claimable),
    );
    send(
        svm,
        upgrade_authority,
        initialize_roots_ix(upgrade_authority),
    );
    for (index, chunk) in tree.roots.chunks(26).enumerate() {
        send(
            svm,
            upgrade_authority,
            write_roots_chunk_ix(upgrade_authority, (index * 26) as u16, chunk),
        );
    }
    for shard in 0..distributor_core::consts::SHARD_COUNT {
        let leaf_count = if shard == leaf.shard_id { 1 } else { 0 };
        send(
            svm,
            upgrade_authority,
            initialize_bitmap_ix(upgrade_authority, shard, leaf_count),
        );
    }
    send(
        svm,
        upgrade_authority,
        finalize_genesis_ix(upgrade_authority, &mint_accounts, tree.roots_commitment),
    );

    SingleLeafSetup {
        shard_id: leaf.shard_id,
        leaf_index: leaf.leaf_index,
        bech32_address: leaf.bech32_address.clone(),
        vesting_schedule_hash: leaf.vesting_schedule_hash,
        proof: leaf.proof.clone(),
        roots_commitment: tree.roots_commitment,
        vault: mint_accounts.vault,
        mint: mint_accounts.mint,
    }
}

pub struct LeafSetupSpec {
    pub liquid_amount: u64,
    pub locked_amount: u64,
    pub vesting_schedule_hash: [u8; 32],
    pub leaf_index: u32,
    pub bitmap_leaf_count: u32,
    pub total_claimable: u64,
}

pub fn build_leaf_setup_from_spec(
    svm: &mut LiteSVM,
    upgrade_authority: &Keypair,
    cosmos: &CosmosKey,
    spec: &LeafSetupSpec,
) -> SingleLeafSetup {
    let compressed_public_key = cosmos.compressed_public_key();
    let bech32_address = cosmos.bech32_address();
    let address_bytes = distributor_core::address::decode_bech32_address(&bech32_address)
        .expect("cosmos bech32 address should decode");
    let shard_id = distributor_core::shard::shard_id(&address_bytes);
    let leaf_hash = distributor_merkle::leaf_hash(&distributor_merkle::LeafFields {
        shard_id,
        leaf_index: spec.leaf_index,
        compressed_public_key,
        bech32_address: &bech32_address,
        liquid_amount: spec.liquid_amount,
        locked_amount: spec.locked_amount,
        vesting_schedule_hash: spec.vesting_schedule_hash,
    });
    let mut roots = vec![
        distributor_core::consts::EMPTY_SHARD_ROOT;
        distributor_core::consts::SHARD_COUNT as usize
    ];
    roots[shard_id as usize] = leaf_hash;

    let mut leaf_counts = vec![0u32; distributor_core::consts::SHARD_COUNT as usize];
    leaf_counts[shard_id as usize] = spec.bitmap_leaf_count;
    let roots_commitment =
        distributor_merkle::roots_commitment(&roots, &leaf_counts, spec.total_claimable);

    deploy_upgradeable_distributor(svm, upgrade_authority);
    let treasury_amount = 1_000_000u64;
    let mint_accounts = mint_and_accounts(
        svm,
        upgrade_authority,
        treasury_amount,
        spec.total_claimable,
        &PROGRAM_ID,
    );

    send(
        svm,
        upgrade_authority,
        initialize_config_ix(upgrade_authority, &mint_accounts, spec.total_claimable),
    );
    send(
        svm,
        upgrade_authority,
        initialize_roots_ix(upgrade_authority),
    );
    for (index, chunk) in roots.chunks(26).enumerate() {
        send(
            svm,
            upgrade_authority,
            write_roots_chunk_ix(upgrade_authority, (index * 26) as u16, chunk),
        );
    }
    for shard in 0..distributor_core::consts::SHARD_COUNT {
        let leaf_count = if shard == shard_id {
            spec.bitmap_leaf_count
        } else {
            0
        };
        send(
            svm,
            upgrade_authority,
            initialize_bitmap_ix(upgrade_authority, shard, leaf_count),
        );
    }
    send(
        svm,
        upgrade_authority,
        finalize_genesis_ix(upgrade_authority, &mint_accounts, roots_commitment),
    );

    SingleLeafSetup {
        shard_id,
        leaf_index: spec.leaf_index,
        bech32_address,
        vesting_schedule_hash: spec.vesting_schedule_hash,
        proof: vec![],
        roots_commitment,
        vault: mint_accounts.vault,
        mint: mint_accounts.mint,
    }
}

pub fn build_leaf_setup_with_schedule(
    svm: &mut LiteSVM,
    upgrade_authority: &Keypair,
    cosmos: &CosmosKey,
    liquid_amount: u64,
    locked_amount: u64,
    schedule: &distributor_vesting::schedule::Schedule,
) -> SingleLeafSetup {
    build_scheduled_leaf_setup_of(
        svm,
        upgrade_authority,
        cosmos,
        liquid_amount,
        locked_amount,
        schedule,
        LeafKind::Revealed,
    )
}

#[allow(clippy::too_many_arguments)]
fn build_scheduled_leaf_setup_of(
    svm: &mut LiteSVM,
    upgrade_authority: &Keypair,
    cosmos: &CosmosKey,
    liquid_amount: u64,
    locked_amount: u64,
    schedule: &distributor_vesting::schedule::Schedule,
    kind: LeafKind,
) -> SingleLeafSetup {
    let record = distributor_merkle::LedgerRecord {
        compressed_public_key: match kind {
            LeafKind::Revealed => Some(cosmos.compressed_public_key()),
            LeafKind::Unrevealed => None,
        },
        bech32_address: cosmos.bech32_address(),
        liquid_amount,
        locked_amount,
        vesting_schedule_hash: schedule.hash(),
    };
    let tree = distributor_merkle::build_sharded_tree(vec![record])
        .expect("single-leaf tree should build");
    let leaf = &tree.leaves[0];

    deploy_upgradeable_distributor(svm, upgrade_authority);
    let total_claimable = liquid_amount + locked_amount;
    let treasury_amount = 1_000_000u64;
    let mint_accounts = mint_and_accounts(
        svm,
        upgrade_authority,
        treasury_amount,
        total_claimable,
        &PROGRAM_ID,
    );

    send(
        svm,
        upgrade_authority,
        initialize_config_ix(upgrade_authority, &mint_accounts, total_claimable),
    );
    send(
        svm,
        upgrade_authority,
        initialize_roots_ix(upgrade_authority),
    );
    for (index, chunk) in tree.roots.chunks(26).enumerate() {
        send(
            svm,
            upgrade_authority,
            write_roots_chunk_ix(upgrade_authority, (index * 26) as u16, chunk),
        );
    }
    for shard in 0..distributor_core::consts::SHARD_COUNT {
        let leaf_count = if shard == leaf.shard_id { 1 } else { 0 };
        send(
            svm,
            upgrade_authority,
            initialize_bitmap_ix(upgrade_authority, shard, leaf_count),
        );
    }
    send(
        svm,
        upgrade_authority,
        finalize_genesis_ix(upgrade_authority, &mint_accounts, tree.roots_commitment),
    );

    SingleLeafSetup {
        shard_id: leaf.shard_id,
        leaf_index: leaf.leaf_index,
        bech32_address: leaf.bech32_address.clone(),
        vesting_schedule_hash: leaf.vesting_schedule_hash,
        proof: leaf.proof.clone(),
        roots_commitment: tree.roots_commitment,
        vault: mint_accounts.vault,
        mint: mint_accounts.mint,
    }
}

pub fn periodic_schedule(
    total: u64,
    start_at: i64,
    period_length_seconds: i64,
    count: usize,
) -> distributor_vesting::schedule::Schedule {
    let per = total / count as u64;
    let mut periods = Vec::with_capacity(count);
    let mut sum = 0u64;
    for index in 0..count {
        let amount = if index + 1 == count { total - sum } else { per };
        sum += amount;
        periods.push(distributor_vesting::schedule::Period {
            length_seconds: period_length_seconds,
            amount,
        });
    }
    distributor_vesting::schedule::Schedule {
        kind: distributor_vesting::schedule::ScheduleKind::Periodic,
        start_at,
        end_at: start_at + period_length_seconds * count as i64,
        periods,
        total,
    }
}

pub fn stage_schedule(
    svm: &mut LiteSVM,
    rent_payer: &Keypair,
    shard_id: u16,
    leaf_index: u32,
    schedule: &distributor_vesting::schedule::Schedule,
) -> Pubkey {
    stage_bytes(
        svm,
        rent_payer,
        shard_id,
        leaf_index,
        &schedule.encode_preimage(),
    )
}

pub fn stage_bytes(
    svm: &mut LiteSVM,
    rent_payer: &Keypair,
    shard_id: u16,
    leaf_index: u32,
    bytes: &[u8],
) -> Pubkey {
    send(
        svm,
        rent_payer,
        stage_init_ix(rent_payer, shard_id, leaf_index, bytes.len() as u32),
    );
    for (index, chunk) in bytes.chunks(SCHEDULE_CHUNK_BYTES).enumerate() {
        let offset = (index * SCHEDULE_CHUNK_BYTES) as u32;
        let instruction = stage_write_ix(rent_payer, shard_id, leaf_index, offset, chunk.to_vec());
        assert_tx_within_limit(std::slice::from_ref(&instruction), &rent_payer.pubkey());
        send(svm, rent_payer, instruction);
    }
    let digest = distributor_core::hash::keccak256(&[bytes]);
    send(
        svm,
        rent_payer,
        stage_finalize_ix(rent_payer, shard_id, leaf_index, digest),
    );
    crate::instructions::find_schedule_buffer_address(shard_id, leaf_index, &rent_payer.pubkey())
}

pub fn build_two_leaf_same_shard_setup(
    svm: &mut LiteSVM,
    upgrade_authority: &Keypair,
    cosmos_a: &CosmosKey,
    cosmos_b: &CosmosKey,
    liquid_amount: u64,
) -> (SingleLeafSetup, SingleLeafSetup) {
    let record = |cosmos: &CosmosKey| distributor_merkle::LedgerRecord {
        compressed_public_key: Some(cosmos.compressed_public_key()),
        bech32_address: cosmos.bech32_address(),
        liquid_amount,
        locked_amount: 0,
        vesting_schedule_hash: distributor_vesting::schedule::ZERO_SCHEDULE_HASH,
    };
    let tree = distributor_merkle::build_sharded_tree(vec![record(cosmos_a), record(cosmos_b)])
        .expect("two-leaf tree should build");
    assert_eq!(
        tree.leaves[0].shard_id, tree.leaves[1].shard_id,
        "callers must pass a same-shard pair"
    );

    deploy_upgradeable_distributor(svm, upgrade_authority);
    let total_claimable = liquid_amount * 2;
    let mint_accounts = mint_and_accounts(
        svm,
        upgrade_authority,
        1_000_000,
        total_claimable,
        &PROGRAM_ID,
    );
    send(
        svm,
        upgrade_authority,
        initialize_config_ix(upgrade_authority, &mint_accounts, total_claimable),
    );
    send(
        svm,
        upgrade_authority,
        initialize_roots_ix(upgrade_authority),
    );
    for (index, chunk) in tree.roots.chunks(26).enumerate() {
        send(
            svm,
            upgrade_authority,
            write_roots_chunk_ix(upgrade_authority, (index * 26) as u16, chunk),
        );
    }
    for shard in 0..distributor_core::consts::SHARD_COUNT {
        let leaf_count = if shard == tree.leaves[0].shard_id {
            2
        } else {
            0
        };
        send(
            svm,
            upgrade_authority,
            initialize_bitmap_ix(upgrade_authority, shard, leaf_count),
        );
    }
    send(
        svm,
        upgrade_authority,
        finalize_genesis_ix(upgrade_authority, &mint_accounts, tree.roots_commitment),
    );

    let setup_of = |leaf: &distributor_merkle::BuiltLeaf| SingleLeafSetup {
        shard_id: leaf.shard_id,
        leaf_index: leaf.leaf_index,
        bech32_address: leaf.bech32_address.clone(),
        vesting_schedule_hash: leaf.vesting_schedule_hash,
        proof: leaf.proof.clone(),
        roots_commitment: tree.roots_commitment,
        vault: mint_accounts.vault,
        mint: mint_accounts.mint,
    };
    (setup_of(&tree.leaves[0]), setup_of(&tree.leaves[1]))
}

pub fn send_withdraw_vested(
    svm: &mut LiteSVM,
    program_id: &Pubkey,
    keys: &CeremonyAddresses,
    payer: &Keypair,
    shard_id: u16,
    leaf_index: u32,
    beneficiary: &Pubkey,
) -> u64 {
    let beneficiary_token = ata(beneficiary, &keys.mint);
    let before = svm
        .get_account(&beneficiary_token)
        .map(|account| {
            SplAccount::unpack_from_slice(&account.data[..SplAccount::LEN])
                .expect("beneficiary ATA should unpack")
                .amount
        })
        .unwrap_or(0);
    let instruction = withdraw_vested_ix_with_program_id(
        program_id,
        keys,
        shard_id,
        leaf_index,
        &payer.pubkey(),
        beneficiary,
        &payer.pubkey(),
    );
    send(svm, payer, instruction);
    let after = read_token_amount(svm, &beneficiary_token);
    after - before
}
