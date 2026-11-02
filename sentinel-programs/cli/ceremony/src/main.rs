#![deny(
    clippy::unwrap_used,
    clippy::expect_used,
    clippy::panic,
    clippy::indexing_slicing,
    clippy::arithmetic_side_effects
)]

mod commands;

use ceremony::usage::exit_code_for;
use clap::{Parser, Subcommand, ValueEnum};
use solana_pubkey::Pubkey;
use std::path::PathBuf;

#[derive(Parser)]
#[command(name = "ceremony", about = "Sentinel launch-ceremony CLI", version = env!("CARGO_PKG_VERSION"))]
struct Cli {
    #[command(subcommand)]
    command: Command,
}

#[derive(Clone, Copy, ValueEnum)]
pub(crate) enum Cluster {
    Devnet,
    Testnet,
    Mainnet,
    Localnet,
}

impl Cluster {
    fn genesis(self) -> (&'static str, &'static str) {
        match self {
            Cluster::Devnet => ("devnet", ceremony::genesis::DEVNET_GENESIS_HASH),
            Cluster::Testnet => ("testnet", ceremony::genesis::TESTNET_GENESIS_HASH),
            Cluster::Mainnet => ("mainnet-beta", ceremony::genesis::MAINNET_GENESIS_HASH),
            Cluster::Localnet => (
                "localnet",
                "no fixed genesis hash: localnet regenerates one on every validator start",
            ),
        }
    }

    fn opposite(self) -> Cluster {
        match self {
            Cluster::Devnet | Cluster::Testnet => Cluster::Mainnet,
            Cluster::Mainnet => Cluster::Devnet,
            Cluster::Localnet => Cluster::Localnet,
        }
    }
}

#[derive(Subcommand)]
enum Command {
    CreateMint {
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_DATASET_DIR",
            help = "claim dataset directory"
        )]
        dataset_dir: PathBuf,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_KEYS_DIR",
            help = "directory holding the ceremony keypairs"
        )]
        keys_dir: PathBuf,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_RPC_URL",
            help = "rpc url of the target cluster"
        )]
        rpc_url: String,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_DISTRIBUTOR_PROGRAM_ID",
            value_name = "address",
            help = "distributor program id"
        )]
        distributor_program_id: Pubkey,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_PRIORITY_FEE_MICRO_LAMPORTS_PER_CU",
            value_name = "amount",
            default_value_t = 0,
            help = "priority fee in micro-lamports per compute unit"
        )]
        priority_fee_micro_lamports_per_cu: u64,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_JSON",
            help = "print step records as json lines"
        )]
        json: bool,
    },
    InitDistributor {
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_DATASET_DIR",
            help = "claim dataset directory"
        )]
        dataset_dir: PathBuf,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_KEYS_DIR",
            help = "directory holding the ceremony keypairs"
        )]
        keys_dir: PathBuf,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_RPC_URL",
            help = "rpc url of the target cluster"
        )]
        rpc_url: String,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_DISTRIBUTOR_PROGRAM_ID",
            value_name = "address",
            help = "distributor program id"
        )]
        distributor_program_id: Pubkey,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_PRIORITY_FEE_MICRO_LAMPORTS_PER_CU",
            value_name = "amount",
            default_value_t = 0,
            help = "priority fee in micro-lamports per compute unit"
        )]
        priority_fee_micro_lamports_per_cu: u64,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_JSON",
            help = "print step records as json lines"
        )]
        json: bool,
    },
    Run {
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_DATASET_DIR",
            help = "claim dataset directory"
        )]
        dataset_dir: PathBuf,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_KEYS_DIR",
            help = "directory holding the ceremony keypairs"
        )]
        keys_dir: PathBuf,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_RPC_URL",
            help = "rpc url of the target cluster"
        )]
        rpc_url: String,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_CLUSTER",
            help = "cluster the rpc url must belong to"
        )]
        cluster: Cluster,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_OTHER_RPC_URL",
            help = "rpc url of the paired cluster, used to check program ids stay distinct"
        )]
        other_rpc_url: Option<String>,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_ALLOW_UNKNOWN_GENESIS",
            help = "accept a genesis hash that matches no known cluster"
        )]
        allow_unknown_genesis: bool,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_PRIORITY_FEE_MICRO_LAMPORTS_PER_CU",
            value_name = "amount",
            default_value_t = 0,
            help = "priority fee in micro-lamports per compute unit"
        )]
        priority_fee_micro_lamports_per_cu: u64,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_DISTRIBUTOR_PROGRAM_ID",
            value_name = "address",
            help = "distributor program id, read from the program keypair file when omitted"
        )]
        distributor_program_id: Option<Pubkey>,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_DA_URL",
            help = "url the claim dataset is published at"
        )]
        da_url: String,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_DVPN_PROGRAM_ID",
            value_name = "address",
            help = "dvpn program id"
        )]
        dvpn_program_id: Option<Pubkey>,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_FEE_TREASURY_OWNER",
            value_name = "address",
            help = "owner of the dvpn fee treasury token account"
        )]
        fee_treasury_owner: Option<Pubkey>,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_SKIP_DVPN",
            help = "skip the dvpn phase"
        )]
        skip_dvpn: bool,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_JSON",
            help = "print step records as json lines"
        )]
        json: bool,
        #[arg(
            long,
            env = "SENTINEL_CEREMONY_DRY_RUN",
            help = "sign and print every transaction without submitting"
        )]
        dry_run: bool,
    },
}

fn main() {
    tracing_subscriber::fmt()
        .with_writer(std::io::stderr)
        .init();
    let cli = Cli::parse();
    let result = match cli.command {
        Command::CreateMint {
            dataset_dir,
            keys_dir,
            rpc_url,
            distributor_program_id,
            priority_fee_micro_lamports_per_cu,
            json,
        } => commands::create_mint(
            &dataset_dir,
            &keys_dir,
            rpc_url,
            distributor_program_id,
            priority_fee_micro_lamports_per_cu,
            json,
        ),
        Command::InitDistributor {
            dataset_dir,
            keys_dir,
            rpc_url,
            distributor_program_id,
            priority_fee_micro_lamports_per_cu,
            json,
        } => commands::init_distributor(
            &dataset_dir,
            &keys_dir,
            rpc_url,
            distributor_program_id,
            priority_fee_micro_lamports_per_cu,
            json,
        ),
        Command::Run {
            dataset_dir,
            keys_dir,
            rpc_url,
            cluster,
            other_rpc_url,
            allow_unknown_genesis,
            priority_fee_micro_lamports_per_cu,
            distributor_program_id,
            da_url,
            dvpn_program_id,
            fee_treasury_owner,
            skip_dvpn,
            json,
            dry_run,
        } => commands::run(
            &dataset_dir,
            &keys_dir,
            rpc_url,
            cluster,
            other_rpc_url,
            allow_unknown_genesis,
            priority_fee_micro_lamports_per_cu,
            distributor_program_id,
            da_url,
            dvpn_program_id,
            fee_treasury_owner,
            skip_dvpn,
            json,
            dry_run,
        ),
    };
    if let Err(error) = result {
        eprintln!("error: {error:?}");
        std::process::exit(exit_code_for(&error));
    }
}

#[cfg(test)]
#[allow(
    unused_imports,
    unused_qualifications,
    clippy::unwrap_used,
    clippy::expect_used,
    clippy::panic,
    clippy::indexing_slicing,
    clippy::arithmetic_side_effects
)]
mod tests {
    use super::*;
    use anyhow::Context as _;
    use ceremony::CeremonyError;
    use ceremony::exit_code;
    use ceremony::runner::{CeremonySigners, HaltedStep, StepRecord, run_steps};
    use ceremony::sender::{AccountReader, RpcSender, Sender, Transport};
    use ceremony::steps::{
        CeremonyAddresses, CeremonyInputs, RentTable, Step, build_ceremony_steps,
    };
    use ceremony::usage::exit_code_for;
    use solana_instruction::Instruction;
    use solana_keypair::Keypair;
    use solana_pubkey::Pubkey;
    use solana_signature::Signature;
    use solana_signer::Signer;
    use solana_transaction::versioned::VersionedTransaction;
    use txv1::{TxConfig, build_signed};
    #[test]
    fn cli_version_is_wired_to_cargo_pkg_version() {
        use clap::CommandFactory;
        assert_eq!(
            Cli::command().get_version(),
            Some(env!("CARGO_PKG_VERSION"))
        );
    }

    #[test]
    fn create_mint_and_init_distributor_parse_ok() {
        let distributor_program_id = Pubkey::new_unique().to_string();
        Cli::try_parse_from([
            "ceremony",
            "create-mint",
            "--dataset-dir",
            "dataset",
            "--keys-dir",
            "keys",
            "--rpc-url",
            "url",
            "--distributor-program-id",
            &distributor_program_id,
        ])
        .expect("create-mint must parse");

        Cli::try_parse_from([
            "ceremony",
            "init-distributor",
            "--dataset-dir",
            "dataset",
            "--keys-dir",
            "keys",
            "--rpc-url",
            "url",
            "--distributor-program-id",
            &distributor_program_id,
            "--priority-fee-micro-lamports-per-cu",
            "5",
        ])
        .expect("init-distributor must parse");
    }
}
