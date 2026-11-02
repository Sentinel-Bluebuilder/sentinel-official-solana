#![deny(clippy::indexing_slicing, clippy::arithmetic_side_effects)]
#![allow(clippy::unwrap_used, clippy::expect_used, clippy::panic)]

use std::path::{Path, PathBuf};

use ceremony::exit_code;
use clap::{Parser, Subcommand};
use solana_sdk::{signature::Keypair, signer::Signer};
use xtask::stage::Stage;
use xtask::{build_tree, ledger, rehearse, synth};

#[derive(Parser)]
#[command(
    name = "xtask",
    about = "Sentinel launch rehearsal and dataset tooling",
    version
)]
struct Cli {
    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    BuildTree {
        #[arg(long)]
        ledger: PathBuf,
        #[arg(long)]
        reconciliation: PathBuf,
        #[arg(long)]
        out_dir: PathBuf,
    },
    SynthesizeLedger {
        #[arg(long)]
        out: PathBuf,
        #[arg(long)]
        count: usize,
    },
    SynthesizeDataset {
        #[arg(long)]
        out_dir: PathBuf,
        #[arg(long)]
        count: usize,
    },
    Rehearse {
        #[arg(long, default_value_t = 40)]
        count: usize,
        #[arg(long)]
        skip_dvpn: bool,
    },
}

fn run_build_tree(
    ledger_path: &Path,
    reconciliation_path: &Path,
    out_dir: &Path,
) -> Result<(), String> {
    let bytes = std::fs::read(ledger_path)
        .map_err(|error| format!("failed to read {}: {error}", ledger_path.display()))?;
    if bytes.len() < 32 {
        return Err(format!(
            "{} is too short to carry a trailing content hash",
            ledger_path.display()
        ));
    }

    #[allow(clippy::indexing_slicing, clippy::arithmetic_side_effects)]
    let ledger_content_hash: [u8; 32] = bytes[bytes.len() - 32..]
        .try_into()
        .expect("slice of len 32");
    let parse_stage = Stage::start("parse-ledger");
    let records =
        ledger::decode_ledger(&bytes).map_err(|error| format!("ledger parse error: {error:?}"))?;
    parse_stage.done(&[("records", &records.len())]);

    let out_build = build_tree::build(&records, ledger_content_hash)
        .map_err(|error| format!("build-tree failed: {error:?}"))?;

    let write_stage = Stage::start("write-output");
    build_tree::write_output(out_dir, &out_build)
        .map_err(|error| format!("failed to write output to {}: {error}", out_dir.display()))?;
    write_stage.done(&[]);

    let verify_stage = Stage::start("self-verify");
    build_tree::require_valid_dataset(out_dir)
        .map_err(|error| format!("self-verify failed: {error}"))?;
    verify_stage.done(&[]);

    let reconcile_stage = Stage::start("reconcile");
    let reconciliation: serde_json::Value =
        serde_json::from_slice(&std::fs::read(reconciliation_path).map_err(|error| {
            format!("failed to read {}: {error}", reconciliation_path.display())
        })?)
        .map_err(|error| format!("failed to parse {}: {error}", reconciliation_path.display()))?;
    let reconciliation_total_claimable = reconciliation
        .get("total_claimable")
        .and_then(|value| value.as_str())
        .ok_or_else(|| {
            format!(
                "{}: missing string field `total_claimable`",
                reconciliation_path.display()
            )
        })?;
    if reconciliation_total_claimable != out_build.attestation.total_claimable.to_string() {
        return Err(format!(
            "reconciliation mismatch: total_claimable {reconciliation_total_claimable} != built {}",
            out_build.attestation.total_claimable
        ));
    }
    let reconciliation_ledger_content_hash = reconciliation
        .get("ledger_content_hash")
        .and_then(|value| value.as_str())
        .ok_or_else(|| {
            format!(
                "{}: missing string field `ledger_content_hash`",
                reconciliation_path.display()
            )
        })?;
    if reconciliation_ledger_content_hash != out_build.attestation.ledger_content_hash {
        return Err(format!(
            "reconciliation mismatch: ledger_content_hash {reconciliation_ledger_content_hash} != built {}",
            out_build.attestation.ledger_content_hash
        ));
    }

    reconcile_stage.done(&[]);

    println!(
        "build-tree OK: {} leaves, roots_commitment {}, wrote {}",
        out_build.attestation.leaf_count,
        out_build.attestation.roots_commitment,
        out_dir.display()
    );
    Ok(())
}

fn run_synthesize_ledger(out: &Path, count: usize) -> Result<(), String> {
    let records: Vec<ledger::ParsedRecord> = synth::records(count)?
        .into_iter()
        .map(|synthetic| synthetic.record)
        .collect();
    let bytes = ledger::encode_ledger(&records);
    std::fs::write(out, &bytes)
        .map_err(|error| format!("failed to write {}: {error}", out.display()))?;
    println!(
        "synthesize-ledger wrote {count} records ({} bytes) to {}",
        bytes.len(),
        out.display()
    );
    Ok(())
}

fn run_synthesize_dataset(out_dir: &Path, count: usize) -> Result<(), String> {
    let parsed: Vec<ledger::ParsedRecord> = synth::records(count)?
        .into_iter()
        .map(|synthetic| synthetic.record)
        .collect();
    let bytes = ledger::encode_ledger(&parsed);
    if bytes.len() < 32 {
        return Err("synthetic ledger is too short to carry a trailing content hash".into());
    }

    #[allow(clippy::indexing_slicing, clippy::arithmetic_side_effects)]
    let ledger_content_hash: [u8; 32] = bytes[bytes.len() - 32..]
        .try_into()
        .expect("slice of len 32");
    let records =
        ledger::decode_ledger(&bytes).map_err(|error| format!("ledger parse error: {error:?}"))?;

    let out_build = build_tree::build(&records, ledger_content_hash)
        .map_err(|error| format!("build-tree failed: {error:?}"))?;
    build_tree::write_output(out_dir, &out_build)
        .map_err(|error| format!("failed to write output to {}: {error}", out_dir.display()))?;
    build_tree::require_valid_dataset(out_dir)
        .map_err(|error| format!("self-verify failed: {error}"))?;

    println!(
        "synthesize-dataset OK: {count} synthetic records -> {} leaves, roots_commitment {}, wrote {}",
        out_build.attestation.leaf_count,
        out_build.attestation.roots_commitment,
        out_dir.display()
    );
    Ok(())
}

fn is_dvpn_report_ok(report: &rehearse::DvpnRehearsalReport) -> bool {
    report.grantee_channel_funded_via_delegate
        && report.grantee_lease_funded_via_delegate
        && report.redeemed_channels_in_one_v1_tx >= 2
        && report.stream_channel_opened_redeemed_closed_by_grantee
        && report.node_heartbeat_by_grantee
        && report.batched_fee_sweep
        && report.payer_was_fee_payer_and_rent_payer
}

fn run_rehearse(count: usize, skip_dvpn: bool) -> Result<(), String> {
    let mut svm = test_support::build_litesvm();
    let upgrade_authority = std::sync::Arc::new(Keypair::new());
    svm.airdrop(&upgrade_authority.pubkey(), 10_000_000_000_000)
        .map_err(|error| format!("airdrop failed: {error:?}"))?;
    let report = rehearse::run_distributor_in_svm(&mut svm, upgrade_authority, count);
    println!(
        "rehearsal OK: finalized={} bitmap_count={} claims(liquid={},unrevealed={},locked={}) \
                 withdrawn={} vault {}->{} build={:.3}s ceremony={:.3}s",
        report.finalized,
        report.bitmap_count,
        report.liquid_claims,
        report.unrevealed_claims,
        report.locked_claims,
        report.withdrawn_amount,
        report.vault_before,
        report.vault_after,
        report.build_seconds,
        report.ceremony_seconds,
    );
    if !report.finalized
        || !report.double_claim_rejected
        || !report.build_self_verify_passed
        || !report.bitmap_capacity_ok
    {
        return Err("rehearsal completed but one or more invariants failed".into());
    }
    if !skip_dvpn {
        let dvpn_upgrade_authority = Keypair::new();
        svm.airdrop(&dvpn_upgrade_authority.pubkey(), 10_000_000_000_000)
            .map_err(|error| format!("dvpn airdrop failed: {error:?}"))?;
        let dvpn_report = rehearse::run_dvpn_in_svm(&mut svm, &dvpn_upgrade_authority)
            .map_err(|error| format!("dvpn rehearsal failed: {error:#}"))?;
        println!(
            "dvpn rehearsal OK: grantee_channel_funded_via_delegate={} \
                     grantee_lease_funded_via_delegate={} redeemed_channels_in_one_v1_tx={} \
                     stream_channel_opened_redeemed_closed_by_grantee={} \
                     node_heartbeat_by_grantee={} batched_fee_sweep={} \
                     payer_was_fee_payer_and_rent_payer={}",
            dvpn_report.grantee_channel_funded_via_delegate,
            dvpn_report.grantee_lease_funded_via_delegate,
            dvpn_report.redeemed_channels_in_one_v1_tx,
            dvpn_report.stream_channel_opened_redeemed_closed_by_grantee,
            dvpn_report.node_heartbeat_by_grantee,
            dvpn_report.batched_fee_sweep,
            dvpn_report.payer_was_fee_payer_and_rent_payer,
        );
        if !is_dvpn_report_ok(&dvpn_report) {
            return Err("dvpn rehearsal completed but one or more invariants failed".into());
        }
    }
    Ok(())
}

fn main() {
    let cli = Cli::parse();
    let is_gate = matches!(&cli.command, Command::Rehearse { .. });
    let result = match cli.command {
        Command::BuildTree {
            ledger,
            reconciliation,
            out_dir,
        } => run_build_tree(&ledger, &reconciliation, &out_dir),
        Command::SynthesizeLedger { out, count } => run_synthesize_ledger(&out, count),
        Command::SynthesizeDataset { out_dir, count } => run_synthesize_dataset(&out_dir, count),
        Command::Rehearse { count, skip_dvpn } => run_rehearse(count, skip_dvpn),
    };
    if let Err(error) = result {
        eprintln!("error: {error}");
        std::process::exit(if is_gate {
            exit_code::GATE_REFUSED
        } else {
            exit_code::OPERATIONAL
        });
    }
}
