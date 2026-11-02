use ceremony::CeremonyError;
use ceremony::artifacts::{DerivedSupply, Reconciliation, derive_supply};
use ceremony::dvpn_preconditions::require_sha512_feature_active;
use ceremony::dvpn_steps as dvpn_ceremony_steps;
use ceremony::runner::{CeremonySigners, run_steps};
use ceremony::sender::{AccountReader, ROOTS_PER_CHUNK, RpcSender, Transport};
use ceremony::steps::{
    CeremonyAddresses, CeremonyInputs, RentTable, build_ceremony_steps, build_finalize_genesis_step,
};
use distributor_dataset::Attestation;
use dvpn_core::args::NodePrices;
use dvpn_core::constants::ALLOWANCE_UNLIMITED;
use dvpn_core::enums::PositionTerms;
use dvpn_core::permissions::*;
use dvpn_core::seeds as dvpn_seeds;
use dvpn_core::voucher::Voucher;
use litesvm::LiteSVM;
use solana_sdk::{
    clock::Clock,
    hash::Hash,
    instruction::Instruction,
    pubkey::Pubkey,
    signature::{Keypair, Signature},
    signer::Signer,
    transaction::VersionedTransaction,
};
use std::sync::Arc;
use std::time::Instant;
use txv1::build_signed;

pub struct DistributorRehearsalReport {
    pub finalized: bool,
    pub bitmap_count: u16,
    pub total_claimable: u64,
    pub vault_before: u64,
    pub vault_after: u64,
    pub total_claimed: u64,
    pub liquid_claims: u32,
    pub unrevealed_claims: u32,
    pub locked_claims: u32,
    pub withdrawn_amount: u64,
    pub double_claim_rejected: bool,
    pub build_self_verify_passed: bool,
    pub bitmap_capacity_ok: bool,
    pub build_seconds: f64,
    pub ceremony_seconds: f64,
    pub roots_per_chunk: usize,
    pub roots_chunk_steps: usize,
    pub priority_fee_micro_lamports_per_cu: u64,
    pub payer: Pubkey,
    pub upgrade_authority: Pubkey,
    pub treasury_amount: u64,
}

pub const REHEARSAL_PRIORITY_FEE_MICRO_LAMPORTS_PER_CU: u64 = 1;

struct SvmTransport<'a>(&'a mut LiteSVM);

impl AccountReader for SvmTransport<'_> {
    fn account_exists(&self, public_key: &Pubkey) -> bool {
        self.0.get_account(public_key).is_some()
    }
    fn account_data(&self, public_key: &Pubkey) -> Option<Vec<u8>> {
        self.0.get_account(public_key).map(|account| account.data)
    }
}

impl Transport for SvmTransport<'_> {
    fn latest_blockhash(&self) -> Result<Hash, CeremonyError> {
        Ok(self.0.latest_blockhash())
    }
    fn submit(&mut self, transaction: &VersionedTransaction) -> Result<Signature, CeremonyError> {
        let signature = transaction.signatures.first().copied().unwrap_or_default();
        self.0
            .send_transaction(transaction.clone())
            .map(|meta| meta.signature)
            .map_err(|error| CeremonyError::TransportFailed {
                signature,
                message: format!("transaction failed: {error:?}"),
            })
    }
}

fn rehearsal_supply_at_halt(total_claimable: u64) -> Result<u64, String> {
    total_claimable
        .checked_mul(2)
        .and_then(|value| value.checked_add(1))
        .ok_or_else(|| {
            format!("synthetic supply at halt overflows u64 for total_claimable {total_claimable}")
        })
}

pub fn rehearsal_supply(
    attestation: &Attestation,
    ledger_content_hash: [u8; 32],
) -> Result<DerivedSupply, String> {
    let total_claimable = attestation.total_claimable;
    let supply_at_halt = rehearsal_supply_at_halt(total_claimable)?;
    let treasury_total = supply_at_halt.checked_sub(total_claimable).ok_or_else(|| {
        format!(
            "synthetic supply at halt {supply_at_halt} underflows total_claimable {total_claimable}"
        )
    })?;
    let reconciliation = Reconciliation {
        supply_at_halt: supply_at_halt.to_string(),
        total_claimable: total_claimable.to_string(),
        treasury_total: treasury_total.to_string(),
        ledger_content_hash: hex::encode(ledger_content_hash),
    };
    derive_supply(attestation, &reconciliation).map_err(|error| error.to_string())
}

fn mint_grown_space(token_metadata: &distributor_mint::create::TokenMetadataParams) -> usize {
    distributor_mint::create::mint_account_space()
        .checked_add(distributor_mint::supply::metadata_tlv_space(token_metadata))
        .expect("mint account space plus metadata TLV space fits usize")
}

fn warp_to_timestamp(svm: &mut LiteSVM, unix_timestamp: i64) {
    let mut clock = svm.get_sysvar::<Clock>();
    clock.unix_timestamp = unix_timestamp;
    svm.set_sysvar::<Clock>(&clock);
}

fn sample_one_of_each(synth: &[crate::synth::SynthRecord]) -> Vec<&crate::synth::SynthRecord> {
    let pick = |predicate: &dyn Fn(&crate::synth::SynthRecord) -> bool| {
        synth
            .iter()
            .find(|candidate| predicate(candidate))
            .expect("synth mix")
    };
    vec![
        pick(&|candidate| {
            candidate.record.compressed_public_key.is_some() && candidate.record.locked_amount == 0
        }),
        pick(&|candidate| candidate.record.compressed_public_key.is_none()),
        pick(&|candidate| candidate.record.locked_amount > 0),
    ]
}

fn built_leaf_from_entry(
    entry: &distributor_dataset::ClaimEntry,
) -> Result<distributor_merkle::BuiltLeaf, String> {
    let address_bytes = distributor_core::address::decode_bech32_address(&entry.bech32_address)
        .map_err(|error| format!("claim entry {}: bad address: {error}", entry.bech32_address))?;
    let compressed_public_key = match &entry.public_key {
        Some(hex_pk) => distributor_dataset::decode_hex_array::<33>(hex_pk).map_err(|error| {
            format!(
                "claim entry {}: bad public key hex: {error}",
                entry.bech32_address
            )
        })?,
        None => distributor_merkle::UNREVEALED_PUBKEY,
    };
    let vesting_schedule_hash = distributor_dataset::decode_hex_array::<32>(
        &entry.vesting_schedule_hash,
    )
    .map_err(|error| {
        format!(
            "claim entry {}: bad vesting_schedule_hash hex: {error}",
            entry.bech32_address
        )
    })?;
    let proof = entry
        .proof
        .iter()
        .map(|hex_text| distributor_dataset::decode_hex_array::<32>(hex_text))
        .collect::<Result<Vec<[u8; 32]>, _>>()
        .map_err(|error| {
            format!(
                "claim entry {}: bad proof hex: {error}",
                entry.bech32_address
            )
        })?;
    let leaf_hash = entry
        .leaf_hash()
        .map_err(|error| format!("claim entry {}: leaf_hash: {error}", entry.bech32_address))?;
    Ok(distributor_merkle::BuiltLeaf {
        shard_id: entry.shard_id,
        leaf_index: entry.leaf_index,
        address_bytes,
        bech32_address: entry.bech32_address.clone(),
        compressed_public_key,
        liquid_amount: entry.liquid_amount,
        locked_amount: entry.locked_amount,
        vesting_schedule_hash,
        leaf_hash,
        proof,
    })
}

fn find_built_leaf(
    out: &crate::build_tree::BuildOutput,
    bech32_address: &str,
) -> Result<distributor_merkle::BuiltLeaf, String> {
    let entry = out
        .shards
        .iter()
        .flat_map(|shard| &shard.entries)
        .find(|entry| entry.bech32_address == bech32_address)
        .ok_or_else(|| format!("no claim entry for {bech32_address} in the built dataset"))?;
    built_leaf_from_entry(entry)
}

pub fn run_distributor_in_svm(
    svm: &mut LiteSVM,
    upgrade_authority: Arc<Keypair>,
    record_count: usize,
) -> DistributorRehearsalReport {
    let synth = crate::synth::records(record_count).expect("synth records out of range");
    let parsed_in: Vec<_> = synth
        .iter()
        .map(|synthetic| synthetic.record.clone())
        .collect();
    let bytes = crate::ledger::encode_ledger(&parsed_in);
    let records = crate::ledger::decode_ledger(&bytes).expect("synthetic ledger round-trips");

    #[allow(clippy::indexing_slicing, clippy::arithmetic_side_effects)]
    let ledger_content_hash: [u8; 32] = bytes[bytes.len() - 32..].try_into().unwrap();

    let t_build = Instant::now();
    let out = crate::build_tree::build(&records, ledger_content_hash).expect("synthetic build");
    let dir_guard = tempfile::TempDir::new().expect("create rehearsal scratch dir");
    let dir = dir_guard.path().to_path_buf();
    crate::build_tree::write_output(&dir, &out).expect("write dataset");
    let build_self_verify_passed = distributor_dataset::require_valid_dataset(&dir).is_ok();
    let build_seconds = t_build.elapsed().as_secs_f64();

    let program_id =
        distributor_test_support::deploy_upgradeable_distributor(svm, &upgrade_authority);
    let total_claimable = out.attestation.total_claimable;

    let payer_keypair = Arc::new(Keypair::new());
    svm.airdrop(&payer_keypair.pubkey(), 10_000_000_000_000)
        .expect("airdrop the rehearsal payer");
    let mint_keypair = Keypair::new();
    let treasury_keypair = Keypair::new();
    let vault_keypair = Keypair::new();
    let keys = CeremonyAddresses {
        upgrade_authority: upgrade_authority.pubkey(),
        payer: payer_keypair.pubkey(),
        mint: mint_keypair.pubkey(),
        treasury: treasury_keypair.pubkey(),
        vault: vault_keypair.pubkey(),
    };
    let signers = CeremonySigners {
        upgrade_authority: Some(Arc::try_unwrap(upgrade_authority).unwrap_or_else(|_| {
            unreachable!("run_distributor_in_svm holds the sole Arc<Keypair> ref")
        })),
        payer: payer_keypair.clone(),
        mint: mint_keypair,
        treasury: treasury_keypair,
        vault: vault_keypair,
    };

    let derived = rehearsal_supply(&out.attestation, ledger_content_hash)
        .expect("rehearsal supply derives from the synthetic artifacts");
    assert_eq!(
        derived.vault_amount, total_claimable,
        "derive_supply must agree with the built tree's leaf sum"
    );
    let inputs = CeremonyInputs {
        program_id,
        token_metadata: distributor_mint::create::TokenMetadataParams {
            name: "Sentinel".into(),
            symbol: "DVPN".into(),
            uri: "https://sentinel.co/metadata.json".into(),
        },
        roots: out.roots.clone(),
        shard_leaf_counts: out.attestation.shard_leaf_counts.clone(),
        vault_amount: derived.vault_amount,
        treasury_amount: derived.treasury_amount,
        expected_roots_commitment: derived.expected_roots_commitment,
    };
    let rent = RentTable {
        mint_at_create_lamports: svm
            .minimum_balance_for_rent_exemption(distributor_mint::create::mint_account_space()),
        mint_with_metadata_lamports: svm
            .minimum_balance_for_rent_exemption(mint_grown_space(&inputs.token_metadata)),
        token_account_lamports: svm
            .minimum_balance_for_rent_exemption(distributor_mint::supply::token_account_space()),
    };
    let t_ceremony = Instant::now();

    let roots_per_chunk = ROOTS_PER_CHUNK;
    let mut steps = build_ceremony_steps(&inputs, &keys, &rent, roots_per_chunk);
    let roots_chunk_steps = steps
        .iter()
        .filter(|step| step.name.starts_with("distributor_write_roots_chunk_"))
        .count();
    steps.push(build_finalize_genesis_step(&inputs, &keys));
    {
        let mut sender = RpcSender::new(
            SvmTransport(&mut *svm),
            &signers.payer,
            signers.upgrade_authority.as_ref(),
            REHEARSAL_PRIORITY_FEE_MICRO_LAMPORTS_PER_CU,
        );
        run_steps(&mut sender, &steps, &signers)
            .unwrap_or_else(|error| panic!("rehearsal ceremony failed: {error}"));
    }
    let ceremony_seconds = t_ceremony.elapsed().as_secs_f64();

    let bitmap_capacity_ok =
        inputs
            .shard_leaf_counts
            .iter()
            .enumerate()
            .all(|(shard_id, &total)| {
                let shard_id =
                    u16::try_from(shard_id).expect("shard count fits u16 (bounded by SHARD_COUNT)");
                distributor_test_support::bitmap_capacity(svm, &program_id, shard_id) >= total
            });

    let cfg = distributor_test_support::read_config(svm);
    let vault_before = distributor_test_support::read_token_amount(svm, &keys.vault);
    let (mut liquid_claims, mut unrevealed_claims, mut locked_claims) = (0u32, 0u32, 0u32);
    let (mut total_claimed, mut withdrawn_amount) = (0u64, 0u64);
    let mut first_claim: Option<Instruction> = None;

    #[allow(clippy::arithmetic_side_effects)]
    for sample in sample_one_of_each(&synth) {
        let beneficiary = Keypair::new();
        let cosmos = distributor_test_support::CosmosKey::from_seed(sample.cosmos_seed);
        let sig = distributor_test_support::sign_claim(
            &cosmos,
            &beneficiary.pubkey(),
            &cfg.roots_commitment,
            &program_id,
        );
        let leaf = find_built_leaf(&out, &sample.record.bech32_address)
            .expect("synth record has a matching claim entry in the built dataset");
        let claim_instruction = distributor_test_support::claim_ix_with_program_id(
            &program_id,
            &keys,
            &leaf,
            &payer_keypair,
            &beneficiary.pubkey(),
            &sig,
            sample.record.liquid_amount,
            sample.record.locked_amount,
            sample.record.schedule.clone(),
        );
        distributor_test_support::send(svm, &payer_keypair, claim_instruction.clone());
        first_claim.get_or_insert(claim_instruction);
        total_claimed += sample.record.liquid_amount;
        match (
            sample.record.compressed_public_key.is_some(),
            sample.record.locked_amount > 0,
        ) {
            (_, true) => {
                locked_claims += 1;
                let schedule = sample
                    .record
                    .schedule
                    .as_ref()
                    .expect("locked record has a schedule");
                warp_to_timestamp(svm, schedule.end_at);
                withdrawn_amount += distributor_test_support::send_withdraw_vested(
                    svm,
                    &program_id,
                    &keys,
                    &payer_keypair,
                    leaf.shard_id,
                    leaf.leaf_index,
                    &beneficiary.pubkey(),
                );
            }
            (true, false) => liquid_claims += 1,
            (false, false) => unrevealed_claims += 1,
        }
    }

    svm.expire_blockhash();
    let double_claim_rejected = {
        let blockhash = svm.latest_blockhash();
        let transaction = build_signed(
            &payer_keypair.pubkey(),
            &[first_claim.unwrap()],
            &[&payer_keypair],
            blockhash,
            &distributor_test_support::HARNESS_TX_CONFIG,
        )
        .expect("double-claim resubmit compiles to a v1 transaction");
        svm.send_transaction(transaction).is_err()
    };

    let cfg = distributor_test_support::read_config(svm);
    DistributorRehearsalReport {
        finalized: cfg.finalized,
        bitmap_count: cfg.bitmap_count,
        total_claimable: cfg.total_claimable,
        vault_before,
        vault_after: distributor_test_support::read_token_amount(svm, &keys.vault),
        total_claimed: total_claimed
            .checked_add(withdrawn_amount)
            .expect("synthetic rehearsal amounts cannot overflow u64"),
        liquid_claims,
        unrevealed_claims,
        locked_claims,
        withdrawn_amount,
        double_claim_rejected,
        build_self_verify_passed,
        bitmap_capacity_ok,
        build_seconds,
        ceremony_seconds,
        roots_per_chunk,
        roots_chunk_steps,
        priority_fee_micro_lamports_per_cu: REHEARSAL_PRIORITY_FEE_MICRO_LAMPORTS_PER_CU,
        payer: keys.payer,
        upgrade_authority: keys.upgrade_authority,
        treasury_amount: inputs.treasury_amount,
    }
}

pub struct DvpnRehearsalReport {
    pub grantee_channel_funded_via_delegate: bool,
    pub grantee_lease_funded_via_delegate: bool,
    pub redeemed_channels_in_one_v1_tx: u32,
    pub stream_channel_opened_redeemed_closed_by_grantee: bool,
    pub node_heartbeat_by_grantee: bool,
    pub batched_fee_sweep: bool,
    pub payer_was_fee_payer_and_rent_payer: bool,
}

pub fn run_dvpn_in_svm(
    svm: &mut LiteSVM,
    upgrade_authority: &Keypair,
) -> anyhow::Result<DvpnRehearsalReport> {
    use dvpn_test_support::{
        assert_solvent, deploy_dvpn, is_account_closed, ix_grant, ix_node, ix_plan, ix_positions,
        ix_provider, ix_subscription, plant_ata, plant_sealed_mint, read_channel, read_config,
        read_lease, read_node, read_provider, read_token_amount, send_expect_ok,
        sign_channel_voucher_with_wallet, sign_lease_voucher_with_wallet,
    };

    let program_id = deploy_dvpn(svm, upgrade_authority);
    let mint = plant_sealed_mint(svm);
    let (config, _bump) = dvpn_seeds::find_config(&program_id);
    let fee_treasury_owner = Pubkey::new_unique();
    let vault = dvpn_ceremony_steps::dvpn_vault_address(&config, &mint);
    let fee_treasury = dvpn_ceremony_steps::fee_treasury_address(&fee_treasury_owner, &mint);

    let payer = Keypair::new();
    svm.airdrop(&payer.pubkey(), 500_000_000_000)
        .map_err(|error| anyhow::anyhow!("airdrop payer: {error:?}"))?;

    let params = dvpn_ceremony_steps::default_initial_config_params();
    let genesis_steps = vec![
        dvpn_ceremony_steps::build_dvpn_create_fee_treasury_step(
            &payer.pubkey(),
            &fee_treasury_owner,
            &mint,
            &config,
        )
        .map_err(|error| anyhow::anyhow!(error))?,
        dvpn_ceremony_steps::build_dvpn_create_vault_step(&payer.pubkey(), &config, &mint),
        dvpn_ceremony_steps::build_dvpn_initialize_config_step(
            &upgrade_authority.pubkey(),
            &program_id,
            &config,
            &mint,
            &vault,
            &fee_treasury,
            &payer.pubkey(),
            &params,
        ),
    ];
    let signers = CeremonySigners {
        upgrade_authority: Some(upgrade_authority.insecure_clone()),
        payer: Arc::new(payer.insecure_clone()),
        mint: Keypair::new(),
        treasury: Keypair::new(),
        vault: Keypair::new(),
    };
    {
        let mut sender = RpcSender::new(
            SvmTransport(&mut *svm),
            &signers.payer,
            signers.upgrade_authority.as_ref(),
            REHEARSAL_PRIORITY_FEE_MICRO_LAMPORTS_PER_CU,
        );
        run_steps(&mut sender, &genesis_steps, &signers)
            .map_err(|error| anyhow::anyhow!(error.to_string()))?;
    }
    assert_solvent(svm, 0);
    require_sha512_feature_active(&SvmTransport(&mut *svm))?;
    {
        let unpause_step =
            dvpn_ceremony_steps::build_dvpn_unpause_step(&program_id, &upgrade_authority.pubkey());
        let mut sender = RpcSender::new(
            SvmTransport(&mut *svm),
            &signers.payer,
            signers.upgrade_authority.as_ref(),
            REHEARSAL_PRIORITY_FEE_MICRO_LAMPORTS_PER_CU,
        );
        run_steps(&mut sender, std::slice::from_ref(&unpause_step), &signers)
            .map_err(|error| anyhow::anyhow!(error.to_string()))?;
    }
    anyhow::ensure!(
        read_config(svm).paused_mask == 0,
        "dvpn_unpause landed but Config.paused_mask != 0"
    );

    let owner = Keypair::new();
    let owner_token = plant_ata(svm, mint, &owner.pubkey(), 200_000_000_000);

    let node_id = [1u8; 32];
    let node_prices = NodePrices {
        channel_price_per_gb: 100,
        channel_price_per_hour: 1_000,
        lease_price_per_hour: 2_000,
    };
    let open_node_ix = ix_node::open_node_ix(
        &owner.pubkey(),
        &payer.pubkey(),
        config,
        mint,
        vault,
        owner_token,
        node_id,
        node_prices,
        0,
        *b"US",
        vec![],
        params.min_node_bond,
    );
    send_expect_ok(svm, &[open_node_ix], &[&payer, &owner]);
    assert_solvent(svm, 0);
    let (node, _) = dvpn_seeds::find_node(&program_id, &owner.pubkey(), &node_id);

    let provider_id = [2u8; 32];
    let open_provider_ix = ix_provider::open_provider_ix(
        &owner.pubkey(),
        &payer.pubkey(),
        config,
        mint,
        vault,
        owner_token,
        provider_id,
        vec![],
        params.min_provider_bond,
    );
    send_expect_ok(svm, &[open_provider_ix], &[&payer, &owner]);
    assert_solvent(svm, 0);
    let (provider, _) = dvpn_seeds::find_provider(&program_id, &owner.pubkey(), &provider_id);

    let node_grantee = Keypair::new();
    let provider_grantee = Keypair::new();
    let user_grantee = Keypair::new();
    for (grantee, permissions) in [
        (&node_grantee, PERM_LIST_NODE),
        (&provider_grantee, PERM_LIST_PROVIDER | PERM_OPEN_LEASE),
        (&user_grantee, PERM_OPEN_CHANNEL | PERM_CLOSE_CHANNEL),
    ] {
        let open_grant_instruction = ix_grant::open_grant_ix(
            &owner.pubkey(),
            &payer.pubkey(),
            grantee.pubkey(),
            Pubkey::default(),
            permissions,
            0,
            ALLOWANCE_UNLIMITED,
            payer.pubkey(),
        );
        send_expect_ok(svm, &[open_grant_instruction], &[&payer, &owner]);
    }
    let node_grant = dvpn_seeds::find_grant(
        &program_id,
        &owner.pubkey(),
        &node_grantee.pubkey(),
        &Pubkey::default(),
    )
    .0;
    let provider_grant = dvpn_seeds::find_grant(
        &program_id,
        &owner.pubkey(),
        &provider_grantee.pubkey(),
        &Pubkey::default(),
    )
    .0;
    let user_grant = dvpn_seeds::find_grant(
        &program_id,
        &owner.pubkey(),
        &user_grantee.pubkey(),
        &Pubkey::default(),
    )
    .0;

    let clock_before_listing = svm.get_sysvar::<Clock>().unix_timestamp;
    let list_node_ix = ix_node::list_node_ix_as_grantee(
        &node_grantee.pubkey(),
        &owner.pubkey(),
        &node_grant,
        node_id,
    );
    send_expect_ok(svm, &[list_node_ix], &[&payer, &node_grantee]);
    let node_after_list = read_node(svm, &node);
    let node_heartbeat_by_grantee = node_after_list.status
        == dvpn_core::enums::ListingStatus::Listed as u8
        && node_after_list.listed_at == clock_before_listing;

    let list_provider_ix = ix_provider::list_provider_ix_as_grantee(
        &provider_grantee.pubkey(),
        &owner.pubkey(),
        &provider_grant,
        provider_id,
    );
    send_expect_ok(svm, &[list_provider_ix], &[&payer, &provider_grantee]);

    let approve_ix = dvpn_core::approve::approve_config_delegate_ix(
        &owner_token,
        &owner.pubkey(),
        &mint,
        &config,
        50_000_000,
        dvpn_test_support::TEST_MINT_DECIMALS,
    );
    send_expect_ok(svm, &[approve_ix], &[&payer, &owner]);

    let escrow_a = 1_000_000u64;
    let escrow_b = 2_000_000u64;
    let channel_a_id = [10u8; 32];
    let channel_b_id = [11u8; 32];

    let payer_lamports_before_channel_a = svm
        .get_account(&payer.pubkey())
        .map(|account| account.lamports)
        .unwrap_or(0);
    let open_channel_a_ix = ix_positions::open_channel_ix_as_grantee_with_accounts(
        config,
        vault,
        mint,
        &owner.pubkey(),
        &user_grantee.pubkey(),
        &user_grant,
        &node,
        &owner_token,
        channel_a_id,
        escrow_a,
        PositionTerms::Voucher {
            voucher_signer: user_grantee.pubkey(),
        },
        &payer.pubkey(),
        &payer.pubkey(),
        &payer.pubkey(),
    );
    let channel_a_fee = send_expect_ok(svm, &[open_channel_a_ix], &[&payer, &user_grantee]).fee;
    assert_solvent(svm, 0);
    let (channel_a, _) = dvpn_seeds::find_channel(&program_id, &owner.pubkey(), &channel_a_id);
    let channel_a_state = read_channel(svm, &channel_a);
    let channel_a_rent = svm
        .get_account(&channel_a)
        .map(|account| account.lamports)
        .unwrap_or(0);
    let payer_lamports_after_channel_a = svm
        .get_account(&payer.pubkey())
        .map(|account| account.lamports)
        .unwrap_or(0);
    let grantee_channel_funded_via_delegate =
        channel_a_state.escrow == escrow_a && channel_a_state.rent_recipient == payer.pubkey();
    let payer_was_fee_payer_and_rent_payer = payer_lamports_before_channel_a
        .saturating_sub(payer_lamports_after_channel_a)
        >= channel_a_fee.saturating_add(channel_a_rent);

    let open_channel_b_ix = ix_positions::open_channel_ix_as_grantee_with_accounts(
        config,
        vault,
        mint,
        &owner.pubkey(),
        &user_grantee.pubkey(),
        &user_grant,
        &node,
        &owner_token,
        channel_b_id,
        escrow_b,
        PositionTerms::Voucher {
            voucher_signer: user_grantee.pubkey(),
        },
        &payer.pubkey(),
        &payer.pubkey(),
        &payer.pubkey(),
    );
    send_expect_ok(svm, &[open_channel_b_ix], &[&payer, &user_grantee]);
    assert_solvent(svm, 0);
    let (channel_b, _) = dvpn_seeds::find_channel(&program_id, &owner.pubkey(), &channel_b_id);
    let channel_b_state = read_channel(svm, &channel_b);

    let cumulative_a = 500_000u64;
    let cumulative_b = 1_000_000u64;
    let sig_a = sign_channel_voucher_with_wallet(
        &user_grantee,
        &channel_a,
        channel_a_state.opened_at,
        cumulative_a,
    );
    let sig_b = sign_channel_voucher_with_wallet(
        &user_grantee,
        &channel_b,
        channel_b_state.opened_at,
        cumulative_b,
    );
    let mut items = vec![
        (
            channel_a,
            Voucher {
                cumulative_amount: cumulative_a,
                signature: sig_a,
            },
        ),
        (
            channel_b,
            Voucher {
                cumulative_amount: cumulative_b,
                signature: sig_b,
            },
        ),
    ];
    items.sort_by_key(|(address, _)| *address);
    let redeem_channels: Vec<Pubkey> = items.iter().map(|(address, _)| *address).collect();
    let redeem_vouchers: Vec<Voucher> = items.into_iter().map(|(_, voucher)| voucher).collect();
    let redeem_vouchers_ix = ix_positions::redeem_channel_vouchers_ix_with_accounts(
        config,
        &payer.pubkey(),
        &node,
        &redeem_channels,
        redeem_vouchers,
    );
    send_expect_ok(svm, &[redeem_vouchers_ix], &[&payer]);
    assert_solvent(svm, 0);

    let redeemed_channels_in_one_v1_tx =
        u32::from(read_channel(svm, &channel_a).redeemed == cumulative_a).saturating_add(
            u32::from(read_channel(svm, &channel_b).redeemed == cumulative_b),
        );

    let escrow_lease = 3_000_000u64;
    let lease_id = [20u8; 32];
    let open_lease_ix = ix_positions::open_lease_ix_as_grantee_with_accounts(
        config,
        vault,
        mint,
        &provider,
        &provider_grantee.pubkey(),
        &provider_grant,
        &node,
        &owner_token,
        lease_id,
        escrow_lease,
        PositionTerms::Voucher {
            voucher_signer: provider_grantee.pubkey(),
        },
        &payer.pubkey(),
        &payer.pubkey(),
    );
    send_expect_ok(svm, &[open_lease_ix], &[&payer, &provider_grantee]);
    assert_solvent(svm, 0);
    let (lease, _) = dvpn_seeds::find_lease(&program_id, &provider, &lease_id);
    let lease_state = read_lease(svm, &lease);
    let grantee_lease_funded_via_delegate =
        lease_state.escrow == escrow_lease && lease_state.rent_recipient == payer.pubkey();

    let lease_cumulative = 1_500_000u64;
    let lease_sig = sign_lease_voucher_with_wallet(
        &provider_grantee,
        &lease,
        lease_state.opened_at,
        lease_cumulative,
    );
    let redeem_lease_ix = ix_positions::redeem_lease_vouchers_ix_with_accounts(
        config,
        &payer.pubkey(),
        &node,
        &[lease],
        vec![Voucher {
            cumulative_amount: lease_cumulative,
            signature: lease_sig,
        }],
    );
    send_expect_ok(svm, &[redeem_lease_ix], &[&payer]);
    assert_solvent(svm, 0);

    let plan_id = [30u8; 32];
    let plan_price = 1_000_000u64;
    let open_plan_ix = ix_plan::open_plan_ix_by(
        &owner.pubkey(),
        None,
        &payer.pubkey(),
        provider,
        plan_id,
        plan_price,
        3_600,
        100,
        5,
        payer.pubkey(),
    );
    send_expect_ok(svm, &[open_plan_ix], &[&payer, &owner]);
    let list_plan_ix = ix_plan::list_plan_ix(&owner.pubkey(), provider, plan_id);
    send_expect_ok(svm, &[list_plan_ix], &[&payer, &owner]);

    let subscriber = Keypair::new();
    let subscriber_owner_token = plant_ata(svm, mint, &subscriber.pubkey(), plan_price * 2);
    let (plan, _) = dvpn_seeds::find_plan(&program_id, &provider, &plan_id);
    let open_subscription_ix = ix_subscription::open_subscription_ix_with_price(
        &subscriber.pubkey(),
        &payer.pubkey(),
        subscriber.pubkey(),
        provider,
        plan,
        subscriber_owner_token,
        config,
        mint,
        vault,
        plan_price,
        3_600,
        100,
        5,
        None,
        payer.pubkey(),
    );
    send_expect_ok(svm, &[open_subscription_ix], &[&payer, &subscriber]);
    assert_solvent(svm, 0);

    let escrow_c = 4_000_000u64;
    let channel_c_id = [12u8; 32];
    let open_channel_c_ix = ix_positions::open_channel_ix_as_grantee_with_accounts(
        config,
        vault,
        mint,
        &owner.pubkey(),
        &user_grantee.pubkey(),
        &user_grant,
        &node,
        &owner_token,
        channel_c_id,
        escrow_c,
        PositionTerms::Stream {
            expected_rate_per_hour: node_prices.channel_price_per_hour,
        },
        &payer.pubkey(),
        &payer.pubkey(),
        &payer.pubkey(),
    );
    send_expect_ok(svm, &[open_channel_c_ix], &[&payer, &user_grantee]);
    assert_solvent(svm, 0);
    let (channel_c, _) = dvpn_seeds::find_channel(&program_id, &owner.pubkey(), &channel_c_id);
    let channel_c_state = read_channel(svm, &channel_c);
    let channel_c_opener_at_open = channel_c_state.opener;

    let now = svm.get_sysvar::<Clock>().unix_timestamp;
    warp_to_timestamp(svm, now.saturating_add(3_600));

    let redeem_stream_ix = ix_positions::redeem_channel_streams_ix_with_accounts(
        config,
        &payer.pubkey(),
        &node,
        &[channel_c],
    );
    send_expect_ok(svm, &[redeem_stream_ix], &[&payer]);
    assert_solvent(svm, 0);

    let close_channel_c_ix = ix_positions::close_channel_ix_with_accounts(
        config,
        &channel_c_state.owner,
        &channel_c,
        &user_grantee.pubkey(),
        ix_positions::find_grant_if_delegated(&channel_c_state.owner, &user_grantee.pubkey()),
        Some(&node),
        &payer.pubkey(),
    );
    send_expect_ok(svm, &[close_channel_c_ix], &[&payer, &user_grantee]);
    assert_solvent(svm, 0);
    let stream_channel_opened_redeemed_closed_by_grantee =
        channel_c_opener_at_open == user_grantee.pubkey() && is_account_closed(svm, &channel_c);

    let node_fees_before = read_node(svm, &node).fees_accrued;
    let provider_fees_before = read_provider(svm, &provider).fees_accrued;
    let fee_treasury_before = read_token_amount(svm, &fee_treasury);
    let sweep_node_ix =
        ix_node::sweep_node_fees_ix(&payer.pubkey(), config, vault, fee_treasury, mint, &[node]);
    let sweep_provider_ix = ix_provider::sweep_provider_fees_ix(
        &payer.pubkey(),
        config,
        vault,
        fee_treasury,
        mint,
        &[provider],
    );
    send_expect_ok(svm, &[sweep_node_ix, sweep_provider_ix], &[&payer]);
    assert_solvent(svm, 0);
    let fee_treasury_after = read_token_amount(svm, &fee_treasury);
    let batched_fee_sweep = node_fees_before > 0
        && provider_fees_before > 0
        && read_node(svm, &node).fees_accrued == 0
        && read_provider(svm, &provider).fees_accrued == 0
        && fee_treasury_after
            == fee_treasury_before
                .saturating_add(node_fees_before)
                .saturating_add(provider_fees_before);

    Ok(DvpnRehearsalReport {
        grantee_channel_funded_via_delegate,
        grantee_lease_funded_via_delegate,
        redeemed_channels_in_one_v1_tx,
        stream_channel_opened_redeemed_closed_by_grantee,
        node_heartbeat_by_grantee,
        batched_fee_sweep,
        payer_was_fee_payer_and_rent_payer,
    })
}
