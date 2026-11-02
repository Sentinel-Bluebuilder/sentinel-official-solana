#![deny(
    clippy::unwrap_used,
    clippy::expect_used,
    clippy::panic,
    clippy::indexing_slicing,
    clippy::arithmetic_side_effects
)]

pub mod compile;
pub mod config;
pub mod fee;
#[cfg(feature = "rpc")]
pub mod preflight;
pub mod size;

pub use compile::{TxError, build_signed, compile, sign};
pub use config::{ConfigError, TxConfig};
pub use fee::priority_fee_lamports;
pub use size::{SizeError, check_within_limit, unsigned_wire_size, wire_size};

pub const MAX_TX_BYTES: usize = solana_message::v1::MAX_TRANSACTION_SIZE;

const _: () = assert!(MAX_TX_BYTES == 4096);
