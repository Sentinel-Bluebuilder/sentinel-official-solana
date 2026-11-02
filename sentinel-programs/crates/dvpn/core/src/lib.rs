#![deny(
    clippy::unwrap_used,
    clippy::expect_used,
    clippy::panic,
    clippy::indexing_slicing,
    clippy::arithmetic_side_effects
)]

pub mod approve;
pub mod args;
pub mod constants;
pub mod enums;
pub mod permissions;
pub mod seeds;
pub mod stream;
pub mod voucher;

#[cfg(not(target_os = "solana"))]
#[allow(clippy::min_ident_chars)]
pub mod id;
