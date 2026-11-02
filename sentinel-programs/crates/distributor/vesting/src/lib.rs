#![deny(
    clippy::unwrap_used,
    clippy::expect_used,
    clippy::panic,
    clippy::indexing_slicing,
    clippy::arithmetic_side_effects
)]

pub mod legacy_dec;
pub mod schedule;
pub mod unlock;

pub use schedule::{Period, Schedule, ScheduleKind};
